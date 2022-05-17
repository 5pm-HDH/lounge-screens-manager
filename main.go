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
	"time"
)

type OnceEvent struct {
	Start    time.Time
	End      time.Time
	Playlist []PlaylistItem
}

type WeeklyEvent struct {
	WeekDay   int
	StartTime int
	EndTime   int
	Playlist  []PlaylistItem
}

type PlaylistItem struct {
	FilePath string
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
	standardPlaylist []PlaylistItem

	ffplayPath string
	ffmpegPath string
	configPath string
	mosaicPath string
)

const (
	ffmpegVideoToMosaicArg = "[0:v] setpts=PTS-STARTPTS, scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:-1:-1:color=black [a0];[1:v] setpts=PTS-STARTPTS, scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:-1:-1:color=black [a1];[2:v] setpts=PTS-STARTPTS, scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:-1:-1:color=black [a2];[a0][a1][a2]xstack=inputs=3:layout=0_0|w0_0|w0+w1_0[out]"
)

func init() {
	onceEventFolderExpression = regexp.MustCompile("[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]")
	weeklyEventFolderExpression = regexp.MustCompile("[1-7]")
	timeRangeFolderExpression = regexp.MustCompile("[0-2][0-9]-[0-2][0-9]")

	videoFileExpression = regexp.MustCompile("[0-9][0-9]-video")
	pictureFileExpression = regexp.MustCompile("[0-9][0-9]-bild")
	bannerPictureFileExpression = regexp.MustCompile("[0-9][0-9]-banner-bild")
	bannerVideoFileExpression = regexp.MustCompile("[0-9][0-9]-banner-video")

	var err error
	ffplayPath, err = exec.LookPath("ffplay")
	if err != nil {
		log.Panicln("unable to find ffplay executable!")
	}

	ffmpegPath, err = exec.LookPath("ffmpeg")
	if err != nil {
		log.Panicln("unable to find ffmpeg executable!")
	}
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

	scanConfigPath(configPath)

	playlist := selectCurrentPlaylist()
	for _, i := range playlist {
		ffplayCmd := exec.Cmd{
			Path: ffplayPath,
			Args: []string{ffplayPath, "-fs", "-autoexit", i.FilePath},
		}
		if err := ffplayCmd.Run(); err != nil {
			log.Printf("failed to play %s with error: %v", i.FilePath, err)
		}
	}
}

func scanConfigPath(configPath string) {
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

func selectCurrentPlaylist() []PlaylistItem {
	now := time.Now()
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
	return files
}

func mediaDirectoryToPlaylist(mediaDir string) (playlist []PlaylistItem) {
	media := sort.StringSlice(listFilesInDirectory(mediaDir))
	media.Sort()

	for _, m := range media {
		if videoFileExpression.MatchString(m) {
			outPath := m + ".mosaic.mov"
			if _, err := os.Stat(outPath); err != os.ErrNotExist {
				ffmpegCmd := &exec.Cmd{
					Path:   ffmpegPath,
					Args:   []string{ffmpegPath, "-i", m, "-i", m, "-i", m, "-filter_complex", ffmpegVideoToMosaicArg, "-map", "[out]", "-c:v", "mjpeg", "-q:v", "3", outPath},
					Stderr: os.Stderr,
				}
				log.Printf(ffmpegCmd.String())
				if err := ffmpegCmd.Run(); err != nil {
					log.Printf("error concerting video to mosaic, skipping: %v", err)
					continue
				}
			} else {
				log.Printf("no need to re-create mosaic for %s", m)
			}
			playlist = append(playlist, PlaylistItem{FilePath: outPath})
		} else if bannerVideoFileExpression.MatchString(m) {
			playlist = append(playlist, PlaylistItem{FilePath: m})
		} else if bannerPictureFileExpression.MatchString(m) {
			// make video
			playlist = append(playlist, PlaylistItem{FilePath: m})
		} else if pictureFileExpression.MatchString(m) {
			// make mosaic video
			playlist = append(playlist, PlaylistItem{FilePath: m})
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
