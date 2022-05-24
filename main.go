package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OnceEvent struct {
	Start    time.Time
	End      time.Time
	Playlist []*PlaylistItem
}

type WeeklyEvent struct {
	WeekDay   int
	StartTime int
	EndTime   int
	Playlist  []*PlaylistItem
}

type PlaylistItem struct {
	FilePath       string
	OrigFilePath   string
	RenderFinished bool
	IsImage        bool
	ImageDuration  string
	RenderCommands []*exec.Cmd
}

func (pi *PlaylistItem) GetPath() string {
	if pi.RenderFinished {
		return pi.FilePath
	}
	return pi.OrigFilePath
}

var (
	onceEventFolderExpression   *regexp.Regexp
	weeklyEventFolderExpression *regexp.Regexp
	timeRangeFolderExpression   *regexp.Regexp
	videoFileExpression         *regexp.Regexp
	bannerVideoFileExpression   *regexp.Regexp
	bannerPictureFileExpression *regexp.Regexp
	pictureFileExpression       *regexp.Regexp

	onceEvents       []OnceEvent
	weeklyEvents     []WeeklyEvent
	standardPlaylist []*PlaylistItem

	mpvPath     string
	ffmpegPath  string
	fehPath     string
	convertPath string
	mogrifyPath string
	rclonePath  string

	configPath string

	videoRenderQueue     chan *PlaylistItem
	imageProcessingQueue chan *PlaylistItem
	cleanupOrphansQueue  chan string

	reloadMutex *sync.Mutex
)

const (
	ffmpegVideoToMosaicArg = "[0:v] setpts=PTS-STARTPTS, scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:-1:-1:color=black [a0];[1:v] setpts=PTS-STARTPTS, scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:-1:-1:color=black [a1];[2:v] setpts=PTS-STARTPTS, scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:-1:-1:color=black [a2];[a0][a1][a2]xstack=inputs=3:layout=0_0|w0_0|w0+w1_0[out]"
)

func init() {
	onceEventFolderExpression = regexp.MustCompile("[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]")
	weeklyEventFolderExpression = regexp.MustCompile("[1-7]")
	timeRangeFolderExpression = regexp.MustCompile("[0-2][0-9]-[0-2][0-9]")

	videoFileExpression = regexp.MustCompile("[0-9][0-9]-video")
	pictureFileExpression = regexp.MustCompile("[0-9][0-9]-bild-([0-9]*)")
	bannerPictureFileExpression = regexp.MustCompile("[0-9][0-9]-banner-bild-([0-9]*)")
	bannerVideoFileExpression = regexp.MustCompile("[0-9][0-9]-banner-video")

	var err error
	mpvPath, err = exec.LookPath("mpv")
	if err != nil {
		log.Panicln("unable to find mpv executable!")
	}

	ffmpegPath, err = exec.LookPath("ffmpeg")
	if err != nil {
		log.Panicln("unable to find ffmpeg executable!")
	}

	fehPath, err = exec.LookPath("feh")
	if err != nil {
		log.Panicln("unable to find feh executable!")
	}

	mogrifyPath, err = exec.LookPath("mogrify")
	if err != nil {
		log.Panicln("unable to find mogrify executable!")
	}

	convertPath, err = exec.LookPath("convert")
	if err != nil {
		log.Panicln("unable to find convert executable!")
	}

	rclonePath, err = exec.LookPath("rclone")
	if err != nil {
		log.Panicln("unable to find rclone executable!")
	}

	videoRenderQueue = make(chan *PlaylistItem, 100)
	imageProcessingQueue = make(chan *PlaylistItem, 100)
	cleanupOrphansQueue = make(chan string, 10)
	reloadMutex = &sync.Mutex{}
}

func main() {
	if len(os.Getenv("ONEDRIVE_PATH")) == 0 {
		log.Panicln("please set the ONEDRIVE_PATH env")
		return
	}
	configPath = os.Getenv("ONEDRIVE_PATH")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Panicln("the provided ONEDRIVE_PATH does not exist!")
		return
	}

	go processVideoRenderQueue()
	defer close(videoRenderQueue)
	go processImageQueue()
	defer close(imageProcessingQueue)
	go cleanupOrphans()
	defer close(cleanupOrphansQueue)

	scanConfigPath()

	// TODO: use cron
	go func() {
		for {
			syncFromOneDrive()
			scanConfigPath()
			log.Println("finished sync")
			log.Println("waiting for tick")
			time.Sleep(30 * time.Second)
			log.Println("tick")
		}
	}()

	for {
		playlist := selectCurrentPlaylist()
		for _, i := range playlist {
			if i.IsImage {
				fehCmd := exec.Cmd{
					Path: fehPath,
					Args: []string{fehPath, "-F", "-Y", "-D", i.ImageDuration, "-Z", "--on-last-slide", "quit", i.GetPath()},
				}
				log.Printf("showing: %s", fehCmd.String())
				if err := fehCmd.Run(); err != nil {
					log.Printf("failed to show %s with error: %v", i.GetPath(), err)
				}
			} else {
				mpvCmd := exec.Cmd{
					Path: mpvPath,
					Args: []string{mpvPath, "--fs", "--hwdec=auto", i.GetPath()},
				}
				log.Printf("playing: %s", mpvCmd.String())
				if err := mpvCmd.Run(); err != nil {
					log.Printf("failed to play %s with error: %v", i.GetPath(), err)
					if i.RenderFinished {
						log.Printf("deleting rendered video %s because it failed to play, retriggering render - %v", i.GetPath(), os.Remove(i.GetPath()))
						i.RenderFinished = false
						videoRenderQueue <- i
					}
				}
			}

		}
	}
}

func processVideoRenderQueue() {
	for pi := range videoRenderQueue {
		if pi.RenderFinished {
			continue
		}
		for _, rc := range pi.RenderCommands {
			if err := rc.Run(); err != nil {
				log.Printf("failed to convert video file %s, skipping due to error: %v", pi.OrigFilePath, err)
				break
			}
		}
		pi.RenderFinished = true
	}
}

func processImageQueue() {
	for pi := range imageProcessingQueue {
		if pi.RenderFinished {
			continue
		}
		for _, rc := range pi.RenderCommands {
			if err := rc.Run(); err != nil {
				log.Printf("failed to convert image file %s, skipping due to error: %v", pi.OrigFilePath, err)
				break
			}
		}
		pi.RenderFinished = true
	}
}

func cleanupOrphans() {
	for o := range cleanupOrphansQueue {
		log.Printf("removing orphaned rendered file: %s, with error: %v", o, os.Remove(o))
	}
}

func syncFromOneDrive() {
	rcloneCmd := exec.Cmd{
		Path: rclonePath,
		Args: []string{rclonePath, "sync", "--exclude=*.mosaic.*", "onedrive:/lsm/", configPath},
	}
	log.Printf("syncing from onedrive: %s", rcloneCmd.String())
	if err := rcloneCmd.Run(); err != nil {
		log.Printf("failed to sync from onedrive with error: %v", err)
	}
}

func scanConfigPath() {
	reloadMutex.Lock()
	defer reloadMutex.Unlock()
	files := listFilesInDirectory(configPath)
	for _, f := range files {
		if f == "syncInProgress.lock" {
			return
		}
	}

	onceEvents = nil
	weeklyEvents = nil

	for _, f := range files {
		if strings.HasSuffix(f, "once") {
			parseOnceEntries(f)
		} else if strings.HasSuffix(f, "weekly") {
			parseWeeklyEntries(f)
		} else if strings.HasSuffix(f, "standard") {
			standardPlaylist = mediaDirectoryToPlaylist(f)
		} else {
			fmt.Printf("ignoring directory/file: %s", f)
		}
	}
}

func selectCurrentPlaylist() []*PlaylistItem {
	reloadMutex.Lock()
	defer reloadMutex.Unlock()

	now := time.Now()
	nowNoZone := now.Format("2006-01-02 15:04")
	now, _ = time.Parse("2006-01-02 15:04", nowNoZone)
	for _, e := range onceEvents {
		if now.After(e.Start) && now.Before(e.End) {
			return e.Playlist
		}
	}

	for _, e := range weeklyEvents {
		if int(now.Weekday()) == e.WeekDay {
			if now.Hour() >= e.StartTime && now.Hour() < e.EndTime {
				return e.Playlist
			}
		}
	}
	return standardPlaylist
}

func listFilesInDirectory(configPath string) []string {
	files, err := filepath.Glob(strings.TrimSuffix(configPath, "/") + "/*")
	if err != nil {
		log.Panicf("error: %v", err)
		return nil
	}
	var filteredFiles []string
	for _, file := range files {
		if strings.HasSuffix(file, ".mosaic.mov") {
			_, err := os.Stat(strings.TrimSuffix(file, ".mosaic.mov"))
			if os.IsNotExist(err) {
				cleanupOrphansQueue <- file
			}
		} else if strings.HasSuffix(file, ".mosaic.jpg") {
			_, err := os.Stat(strings.TrimSuffix(file, ".mosaic.jpg"))
			if os.IsNotExist(err) {
				cleanupOrphansQueue <- file
			}
		} else {
			filteredFiles = append(filteredFiles, file)
		}
	}

	return filteredFiles
}

func mediaDirectoryToPlaylist(mediaDir string) (playlist []*PlaylistItem) {
	media := sort.StringSlice(listFilesInDirectory(mediaDir))
	media.Sort()

	for _, m := range media {
		if videoFileExpression.MatchString(m) {
			outPath := m + ".mosaic.mov"
			exists := false
			if _, err := os.Stat(outPath); err == nil {
				exists = true
				log.Printf("no need to re-create mosaic for video %s", m)
			}
			pi := &PlaylistItem{
				FilePath:       outPath,
				OrigFilePath:   m,
				RenderFinished: exists,
				IsImage:        false,
				RenderCommands: []*exec.Cmd{{
					Path:   ffmpegPath,
					Args:   []string{ffmpegPath, "-n", "-i", m, "-i", m, "-i", m, "-filter_complex", ffmpegVideoToMosaicArg, "-map", "[out]", "-c:v", "mjpeg", "-q:v", "3", "-r", "25", outPath},
					Stdout: os.Stdout,
					Stderr: os.Stderr,
				}},
			}
			playlist = append(playlist, pi)
			if !pi.RenderFinished {
				videoRenderQueue <- pi
			}
		} else if bannerVideoFileExpression.MatchString(m) {
			outPath := m + ".mosaic.mov"
			exists := false
			if _, err := os.Stat(outPath); err == nil {
				exists = true
				log.Printf("no need to re-create video for banner video %s", m)
			}
			pi := &PlaylistItem{
				FilePath:       outPath,
				OrigFilePath:   m,
				RenderFinished: exists,
				IsImage:        false,
				RenderCommands: []*exec.Cmd{{
					Path:   ffmpegPath,
					Args:   []string{ffmpegPath, "-n", "-i", m, "-c:v", "mjpeg", "-q:v", "3", "-r", "25", "-vf", "scale=5760:1080:force_original_aspect_ratio=decrease,pad=5760:1080:-1:-1:color=black", outPath},
					Stdout: os.Stdout,
					Stderr: os.Stderr,
				}},
			}
			playlist = append(playlist, pi)
			if !pi.RenderFinished {
				videoRenderQueue <- pi
			}
		} else if bannerPictureFileExpression.MatchString(m) {
			outPath := m + ".mosaic.jpg"
			dur := bannerPictureFileExpression.FindStringSubmatch(m)[1]
			exists := false
			if _, err := os.Stat(outPath); err == nil {
				exists = true
				log.Printf("no need to re-size banner picture %s", m)
			}
			pi := &PlaylistItem{
				FilePath:       outPath,
				OrigFilePath:   m,
				RenderFinished: exists,
				IsImage:        true,
				ImageDuration:  dur,
				RenderCommands: []*exec.Cmd{
					{
						Path:   mogrifyPath,
						Args:   []string{mogrifyPath, "-scale", "5760x1080", "-background", "black", "-extent", "5760x1080", "-gravity", "center", m},
						Stdout: os.Stdout,
						Stderr: os.Stderr,
					},
					{
						Path:   convertPath,
						Args:   []string{convertPath, m, "-format", "jpg", outPath},
						Stdout: os.Stdout,
						Stderr: os.Stderr,
					},
				},
			}
			playlist = append(playlist, pi)
			if !pi.RenderFinished {
				imageProcessingQueue <- pi
			}
		} else if pictureFileExpression.MatchString(m) {
			outPath := m + ".mosaic.jpg"
			dur := pictureFileExpression.FindStringSubmatch(m)[1]
			exists := false
			if _, err := os.Stat(outPath); err == nil {
				exists = true
				log.Printf("no need to re-create mosaic video for picture %s", m)
			}
			pi := &PlaylistItem{
				FilePath:       outPath,
				OrigFilePath:   m,
				RenderFinished: exists,
				IsImage:        true,
				ImageDuration:  dur,
				RenderCommands: []*exec.Cmd{
					{
						Path:   mogrifyPath,
						Args:   []string{mogrifyPath, "-scale", "1920x1080", "-background", "black", "-extent", "1920x1080", "-gravity", "center", m},
						Stdout: os.Stdout,
						Stderr: os.Stderr,
					},
					{
						Path:   convertPath,
						Args:   []string{convertPath, m, m, m, "+append", "-format", "jpg", outPath},
						Stdout: os.Stdout,
						Stderr: os.Stderr,
					},
				},
			}
			playlist = append(playlist, pi)
			if !pi.RenderFinished {
				imageProcessingQueue <- pi
			}
		} else {
			log.Printf("skipping invalid file: %s", m)
		}
	}

	return playlist
}

func parseOnceEntries(onceDir string) {
	dates := listFilesInDirectory(onceDir)
	for _, f := range dates {
		dayMatch := onceEventFolderExpression.MatchString(f)
		if !dayMatch {
			log.Printf("skipping malformatted directory: %s", f)
			continue
		}
		day := onceEventFolderExpression.FindStringSubmatch(f)[0]

		times := listFilesInDirectory(f)
		for _, t := range times {
			strippedT := strings.TrimPrefix(t, f+"/")

			timeMatch := timeRangeFolderExpression.MatchString(strippedT)
			if !timeMatch {
				log.Printf("skipping malformatted directory: %s", strippedT)
				continue
			}

			timeRange := timeRangeFolderExpression.FindStringSubmatch(strippedT)[0]
			timeRangeParts := strings.Split(timeRange, "-")
			startTime, _ := time.Parse("2006-01-02 15", day+" "+timeRangeParts[0])
			endTime, _ := time.Parse("2006-01-02 15", day+" "+timeRangeParts[1])

			if endTime.Before(time.Now()) {
				log.Printf("skipping past event: %s", endTime.Format(time.RFC3339))
				continue
			}

			onceEvents = append(onceEvents, OnceEvent{
				Start:    startTime,
				End:      endTime,
				Playlist: mediaDirectoryToPlaylist(t),
			})
		}
	}
}

func parseWeeklyEntries(weeklyDir string) {
	weekDays := listFilesInDirectory(weeklyDir)
	for _, wd := range weekDays {
		weekDayMatch := weeklyEventFolderExpression.MatchString(wd)
		if !weekDayMatch {
			log.Printf("skipping malformatted directory: %s", wd)
			continue
		}
		weekDay, _ := strconv.Atoi(weeklyEventFolderExpression.FindStringSubmatch(wd)[0])

		times := listFilesInDirectory(wd)
		for _, t := range times {
			strippedT := strings.TrimPrefix(t, wd+"/")

			timeMatch := timeRangeFolderExpression.MatchString(strippedT)
			if !timeMatch {
				log.Printf("skipping malformatted directory: %s", strippedT)
				continue
			}

			timeRange := timeRangeFolderExpression.FindStringSubmatch(strippedT)[0]
			timeRangeParts := strings.Split(timeRange, "-")

			start, _ := strconv.Atoi(timeRangeParts[0])
			end, _ := strconv.Atoi(timeRangeParts[1])

			weeklyEvents = append(weeklyEvents, WeeklyEvent{
				WeekDay:   weekDay % 7,
				StartTime: start,
				EndTime:   end,
				Playlist:  mediaDirectoryToPlaylist(t),
			})
		}
	}
}
