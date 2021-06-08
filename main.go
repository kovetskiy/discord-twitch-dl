package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/reconquest/karma-go"
	"github.com/reconquest/pkg/log"

	"github.com/bwmarrin/discordgo"
	"mvdan.cc/xurls/v2"
)

func main() {
	token := os.Getenv("TOKEN")

	ds, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf(err, "discord session")
	}
	defer ds.Close()

	// ds.Identify.Intents = discordgo.IntentsGuildMembers
	ds.AddHandler(handle)

	err = ds.Open()
	if err != nil {
		log.Fatalf(err, "discord open")
	}

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
}

func handle(session *discordgo.Session, msg *discordgo.MessageCreate) {
	if msg.Author.ID == session.State.User.ID {
		return
	}

	url.Parse(msg.Content)

	parser := xurls.Strict()

	allURLs := parser.FindAllString(msg.Content, -1)
	if len(allURLs) == 0 {
		return
	}

	urls := []string{}
	for _, url := range allURLs {
		if strings.Contains(url, "twitch.tv") {
			urls = append(urls, url)
		}
	}

	if len(urls) == 0 {
		return
	}

	for _, url := range urls {
		clip, err := getClip(url)
		if err != nil {
			log.Errorf(err, "download clip: %s", url)
			continue
		}

		file, err := ioutil.TempFile(os.TempDir(), "twitch-clip.")
		if err != nil {
			return
		}

		defer os.Remove(file.Name())
		defer file.Close()

		err = download(clip.DownloadURL, file)
		if err != nil {
			log.Errorf(err, "download: %s %s", url, clip.DownloadURL)
			continue
		}

		file.Seek(0, 0)

		_, err = session.ChannelFileSend(
			msg.ChannelID,
			clip.Broadcaster+": "+clip.Title+".mp4",
			file,
		)
		if err != nil {
			log.Errorf(err, "file send")
			continue
		}

		err = file.Close()
		if err != nil {
			log.Errorf(err, "file close")
			continue
		}
	}
}

type Clip struct {
	Title       string `json:"title"`
	Broadcaster string `json:"broadcaster"`
	DownloadURL string `json:"download_url"`
}

func getClip(clip string) (*Clip, error) {
	values := map[string]string{}
	values["clip_url"] = clip

	buffer := bytes.NewBuffer(nil)
	err := json.NewEncoder(buffer).Encode(values)
	if err != nil {
		return nil, karma.Format(err, "json encode")
	}

	request, err := http.NewRequest(
		"POST",
		"https://clipr.xyz/api/grabclip",
		buffer,
	)
	if err != nil {
		return nil, karma.Format(err, "new request")
	}

	request.Header.Add("x-requested-with", "XMLHttpRequest")
	request.Header.Add("content-type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, karma.Format(err, "http post")
	}

	var result Clip
	err = json.NewDecoder(response.Body).Decode(&result)
	if err != nil {
		return nil, karma.Format(err, "json decode")
	}

	if result.DownloadURL == "" {
		return nil, errors.New("empty url")
	}

	result.DownloadURL = "https:" + result.DownloadURL

	return &result, nil
}

func download(url string, file *os.File) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}
