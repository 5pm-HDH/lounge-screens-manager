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
	RenderCommand  *exec.Cmd
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

	mpvPath    string
	ffmpegPath string
	configPath string
	mosaicPath string

	renderQueue chan *PlaylistItem

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

	renderQueue = make(chan *PlaylistItem, 100)
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

	if len(os.Getenv("MOSAIC_PATH")) == 0 {
		log.Panicln("please set the MOSAIC_PATH env")
		return
	}
	mosaicPath = os.Getenv("MOSAIC_PATH")

	if _, err := os.Stat(mosaicPath); os.IsNotExist(err) {
		log.Panicln("the provided MOSAIC_PATH does not exist!")
		return
	}

	go processRenderQueue()

	scanConfigPath(configPath)
	go func() {
		for range time.NewTimer(time.Minute).C {
			scanConfigPath(configPath)
		}
	}()

	for {
		playlist := selectCurrentPlaylist()
		for _, i := range playlist {
			ffplayCmd := exec.Cmd{
				Path: mpvPath,
				Args: []string{mpvPath, "--fs", "--hwdec=auto", i.GetPath()},
			}
			log.Printf("playing: %s", ffplayCmd.String())
			if err := ffplayCmd.Run(); err != nil {
				log.Printf("failed to play %s with error: %v", i.GetPath(), err)
			}
		}
	}

	close(renderQueue)
}

func processRenderQueue() {
	for pi := range renderQueue {
		if pi.RenderFinished {
			continue
		}
		if err := pi.RenderCommand.Run(); err != nil {
			log.Printf("failed to convert media file %s, skipping due to error: %v", pi.OrigFilePath, err)
		}
		pi.RenderFinished = true
	}
}

func scanConfigPath(configPath string) {
	reloadMutex.Lock()
	defer reloadMutex.Unlock()

	onceEvents = nil
	weeklyEvents = nil

	files := listFilesInDirectory(configPath)

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
		if !strings.HasSuffix(file, ".mosaic.mov") {
			filteredFiles = append(filteredFiles, file)
		}
	}

	return filteredFiles
}

func mediaDirectoryToPlaylist(mediaDir string) (playlist []*PlaylistItem) {
	media := sort.StringSlice(listFilesInDirectory(mediaDir))
	media.Sort()

	for _, m := range media {
		outPath := m + ".mosaic.mov"
		if videoFileExpression.MatchString(m) {
			exists := false
			if _, err := os.Stat(outPath); err == nil {
				exists = true
				log.Printf("no need to re-create mosaic for video %s", m)
			}
			pi := &PlaylistItem{
				FilePath:       outPath,
				OrigFilePath:   m,
				RenderFinished: exists,
				RenderCommand: &exec.Cmd{
					Path:   ffmpegPath,
					Args:   []string{ffmpegPath, "-n", "-i", m, "-i", m, "-i", m, "-filter_complex", ffmpegVideoToMosaicArg, "-map", "[out]", "-c:v", "mjpeg", "-q:v", "3", outPath},
					Stdout: os.Stdout,
					Stderr: os.Stderr,
				},
			}
			playlist = append(playlist, pi)
			if !pi.RenderFinished {
				renderQueue <- pi
			}
		} else if bannerVideoFileExpression.MatchString(m) {
			playlist = append(playlist, &PlaylistItem{
				FilePath:       m,
				OrigFilePath:   m,
				RenderFinished: true,
				RenderCommand:  nil,
			})
		} else if bannerPictureFileExpression.MatchString(m) {
			dur := bannerPictureFileExpression.FindStringSubmatch(m)[1]
			exists := false
			if _, err := os.Stat(outPath); err == nil {
				exists = true
				log.Printf("no need to re-create video for banner picture %s", m)
			}
			pi := &PlaylistItem{
				FilePath:       outPath,
				OrigFilePath:   m,
				RenderFinished: exists,
				RenderCommand: &exec.Cmd{
					Path:   ffmpegPath,
					Args:   []string{ffmpegPath, "-n", "-loop", "1", "-i", m, "-c:v", "mjpeg", "-q:v", "3", "-t", dur, "-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:-1:-1:color=black", outPath},
					Stdout: os.Stdout,
					Stderr: os.Stderr,
				},
			}
			playlist = append(playlist, pi)
			if !pi.RenderFinished {
				renderQueue <- pi
			}
		} else if pictureFileExpression.MatchString(m) {
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
				RenderCommand: &exec.Cmd{
					Path:   ffmpegPath,
					Args:   []string{ffmpegPath, "-n", "-loop", "1", "-i", m, "-loop", "1", "-i", m, "-loop", "1", "-i", m, "-filter_complex", ffmpegVideoToMosaicArg, "-map", "[out]", "-c:v", "mjpeg", "-q:v", "3", "-t", dur, outPath},
					Stdout: os.Stdout,
					Stderr: os.Stderr,
				},
			}
			playlist = append(playlist, pi)
			if !pi.RenderFinished {
				renderQueue <- pi
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
