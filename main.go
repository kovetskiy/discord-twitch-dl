package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/reconquest/karma-go"
	"github.com/reconquest/pkg/log"
	"gopkg.in/tucnak/telebot.v2"

	"github.com/bwmarrin/discordgo"
	"mvdan.cc/xurls/v2"
)

type Handler struct {
	ds   *discordgo.Session
	tg   *telebot.Bot
	chat *telebot.Chat
}

func main() {
	var (
		discordToken  = stringEnv("DISCORD_TOKEN")
		telegramToken = stringEnv("TELEGRAM_TOKEN")
		telegramChat  = intEnv("TELEGRAM_CHAT")
	)

	ds, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		log.Fatalf(err, "discord session")
	}
	defer ds.Close()

	// ds.Identify.Intents = discordgo.IntentsGuildMembers

	err = ds.Open()
	if err != nil {
		log.Fatalf(err, "discord open")
	}

	tg, err := telebot.NewBot(telebot.Settings{
		Token:  telegramToken,
		Poller: &telebot.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		log.Fatalf(err, "telegram connect")
	}

	handler := &Handler{
		ds:   ds,
		tg:   tg,
		chat: &telebot.Chat{ID: int64(telegramChat)},
	}

	ds.AddHandler(handler.Handle)

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
}

func (handler *Handler) Handle(
	session *discordgo.Session,
	msg *discordgo.MessageCreate,
) {
	if msg.Author.ID == session.State.User.ID {
		return
	}

	if !strings.HasPrefix(msg.Content, "-archive ") {
		return
	}

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
		log.Infof(nil, "find link for %s", url)

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

		log.Infof(nil, "download %s", clip.DownloadURL)

		err = download(clip.DownloadURL, file)
		if err != nil {
			log.Errorf(err, "download: %s %s", url, clip.DownloadURL)
			continue
		}

		file.Seek(0, 0)

		info, err := file.Stat()
		if err != nil {
			log.Errorf(err, "file stat")
			continue
		}

		err = file.Close()
		if err != nil {
			log.Errorf(err, "file close")
			continue
		}

		log.Infof(nil, "file size %d", info.Size())
		log.Infof(nil, "file send")

		video, err := handler.tg.Send(handler.chat, &telebot.Video{
			FileName: clip.Broadcaster + ": " + clip.Title + ".mp4",
			File:     telebot.FromDisk(file.Name()),
			Caption: stringLimit(
				fmt.Sprintf("%s: %s", clip.Broadcaster, clip.Title),
				1024,
			),
		})
		if err != nil {
			log.Errorf(err, "telegram send video")
			continue
		}

		videoLink := getPostLink(video)
		_, err = session.ChannelMessageSendComplex(
			msg.ChannelID,
			&discordgo.MessageSend{
				Content: fmt.Sprintf(
					"%s\n%s\n%s",
					clip.Title,
					clip.Broadcaster,
					videoLink,
				),
			},
		)
		if err != nil {
			log.Errorf(err, "discord send link")
			continue
		}

		log.Infof(nil, "finished %s", url)
	}
}

func getPostLink(msg *telebot.Message) string {
	return fmt.Sprintf("https://t.me/%s/%d", msg.Chat.Username, msg.ID)
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

func intEnv(key string) int {
	value := stringEnv(key)

	result, err := strconv.Atoi(value)
	if err != nil {
		log.Fatalf(err, "string to int: %s", key)
	}

	return result
}

func stringEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		log.Fatalf(nil, "no env %q specified", key)
	}

	return value
}

func durationEnv(key string) time.Duration {
	value := stringEnv(key)

	duration, err := time.ParseDuration(value)
	if err != nil {
		log.Fatalf(err, "parse duration: %s for %s", value, key)
	}

	return duration
}

func stringLimit(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
