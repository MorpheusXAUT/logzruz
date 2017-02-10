package logzruz

import (
	"bytes"
	"encoding/json"
	"github.com/Sirupsen/logrus"
	"github.com/juju/errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	Version                = "1.0.0"
	DefaultUserAgent       = "logzruz v" + Version + " - github.com/MorpheusXAUT/logzruz"
	DefaultForceFlushField = "logzruzForceFlush"
	LogzioDefaultURL       = "https://listener.logz.io:8071/"
)

type Hook struct {
	buffer       [][]byte
	bufferTicker *time.Ticker
	bufferMutex  sync.RWMutex
	options      HookOptions
	preparedURL  string
	stop         chan bool
}

type HookOptions struct {
	App             string
	BufferCount     int
	BufferInterval  time.Duration
	Client          *http.Client
	Context         logrus.Fields
	ForceFlushField string
	ForceFlushLevel logrus.Level
	Token           string
	URL             string
	UserAgent       string
}

func NewHook(options HookOptions) (*Hook, error) {
	if len(options.Token) == 0 {
		return nil, errors.NotValidf("invalid logz.io token")
	}

	if options.BufferInterval.Seconds() <= 0 {
		options.BufferInterval = time.Second * 10
	}
	if options.Client == nil {
		options.Client = http.DefaultClient
	}
	if len(options.ForceFlushField) == 0 {
		options.ForceFlushField = DefaultForceFlushField
	}
	if len(options.URL) == 0 {
		options.URL = LogzioDefaultURL
	}
	if len(options.UserAgent) == 0 {
		options.UserAgent = DefaultUserAgent
	}

	u, err := url.Parse(options.URL)
	if err != nil {
		return nil, errors.Annotate(err, "failed to parse base URL")
	}
	q := u.Query()
	q.Set("token", options.Token)
	if len(options.App) > 0 {
		q.Set("type", options.App)
	}
	u.RawQuery = q.Encode()

	hook := &Hook{
		buffer:       make([][]byte, 0),
		bufferTicker: time.NewTicker(options.BufferInterval),
		bufferMutex:  sync.RWMutex{},
		options:      options,
		preparedURL:  u.String(),
		stop:         make(chan bool),
	}

	go hook.bufferTickerLoop()

	return hook, nil
}

func (hook *Hook) Fire(entry *logrus.Entry) error {
	data := logrus.Fields{
		"level":      uint8(entry.Level),
		"levelName":  entry.Level.String(),
		"message":    entry.Message,
		"@timestamp": entry.Time.Format(time.RFC3339Nano),
	}

	for k, v := range hook.options.Context {
		if _, ok := data[k]; ok {
			continue
		}

		switch v := v.(type) {
		case error:
			data[k] = v.Error()
			break
		default:
			data[k] = v
			break
		}
	}

	forceFlush := false
	for k, v := range entry.Data {
		if strings.EqualFold(k, hook.options.ForceFlushField) {
			forceFlush = true
		}
		if _, ok := data[k]; ok {
			continue
		}

		switch v := v.(type) {
		case error:
			data[k] = v.Error()
			break
		default:
			data[k] = v
			break
		}
	}

	message, err := json.Marshal(data)
	if err != nil {
		return errors.NewNotValid(err, "failed to marshal log message to JSON")
	}

	hook.bufferMutex.Lock()
	hook.buffer = append(hook.buffer, message)
	bufferSize := len(hook.buffer)
	hook.bufferMutex.Unlock()

	if !forceFlush && bufferSize < hook.options.BufferCount && entry.Level > hook.options.ForceFlushLevel {
		return nil
	}

	err = hook.flushBuffer()
	if err != nil {
		return errors.Annotate(err, "buffer count triggered flushing")
	}

	return nil
}

func (hook *Hook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
	}
}

func (hook *Hook) Flush() error {
	err := hook.flushBuffer()
	if err != nil {
		return errors.Annotate(err, "failed to manually flush log buffer")
	}

	return nil
}

func (hook *Hook) Shutdown() error {
	hook.bufferTicker.Stop()
	hook.stop <- true

	err := hook.flushBuffer()
	if err != nil {
		return errors.Annotate(err, "failed to force buffer flush upon shutdown")
	}

	return nil
}

func (hook *Hook) flushBuffer() error {
	hook.bufferMutex.Lock()
	var payload bytes.Buffer
	for _, message := range hook.buffer {
		payload.Write(message)
		payload.WriteRune('\n')
	}

	hook.buffer = make([][]byte, 0)
	hook.bufferMutex.Unlock()

	req, err := http.NewRequest(http.MethodPost, hook.preparedURL, &payload)
	if err != nil {
		return errors.Annotate(err, "failed to create logz.io bulk request")
	}
	req.Header.Add("User-Agent", hook.options.UserAgent)

	resp, err := hook.options.Client.Do(req)
	if err != nil {
		return errors.Annotate(err, "failed to send logz.io bulk request")
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusBadRequest:
		return errors.BadRequestf("logz.io bulk request was malformed")
	case http.StatusUnauthorized:
		return errors.Unauthorizedf("logz.io token is unauthorized")
	case http.StatusRequestEntityTooLarge:
		return errors.BadRequestf("logz.io bulk request was too large")
	default:
		return errors.BadRequestf("received status %q from logz.io", resp.Status)
	}
}

func (hook *Hook) bufferTickerLoop() {
	for {
		select {
		case _, ok := <-hook.bufferTicker.C:
			if !ok {
				return
			}

			hook.bufferMutex.RLock()
			bufferSize := len(hook.buffer)
			hook.bufferMutex.RUnlock()

			if bufferSize > 0 {
				err := hook.flushBuffer()
				if err != nil {
					log.Printf("logzruz encountered an error while flushing the log buffer: %v\n", err)
					break
				}
			}
			break
		case <-hook.stop:
			return
		}
	}
}
