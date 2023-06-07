package logzruz

import (
	"bytes"
	"encoding/json"
	"github.com/juju/errors"
	"github.com/sirupsen/logrus"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// Version of the logzruz library
	Version = "1.0.0"
	// DefaultBufferCount defines the default number of messages to buffer before flushing logs
	DefaultBufferCount = 10
	// DefaultBufferInterval defines the default interval to check for messages to flush if not explicitly specified
	DefaultBufferInterval = time.Second * 10
	// DefaultForceFlushField to use for triggering a buffer flush forcefully if not explicitly specified
	DefaultForceFlushField = "logzruzForceFlush"
	// DefaultUserAgent to use for HTTP requests if not explicitly specified
	DefaultUserAgent = "logzruz v" + Version + " - github.com/MorpheusXAUT/logzruz"
	// LogzioDefaultURL specifies the default URL of the logz.io service to use
	LogzioDefaultURL = "https://listener.logz.io:8071/"
)

// Hook represents a logrus hook for shipping logs to logz.io services.
// Messages are buffered until a user-defined limit has been reached or sent on a regular interval.
type Hook struct {
	buffer       [][]byte
	bufferTicker *time.Ticker
	bufferMutex  sync.RWMutex
	options      HookOptions
	preparedURL  string
	stop         chan bool
}

// HookOptions contains all options and settings required to configure the hook.
// Applications only need to provide a Token, although the other options can be used to further modify the library's behaviour.
// If not provided, some options will be configured using defaults.
type HookOptions struct {
	// App "name" to specify as a type for logz.io requests
	App string
	// BufferCount specifies the number of messages to buffer before flushing it (if not triggered by timed triggers before). Setting this value to -1 will disable buffering and instantly send messages (default 10)
	BufferCount int
	// BufferInterval specifies the duration to wait between each check for new buffered messages to send (default 10s)
	BufferInterval time.Duration
	// Client specifies the HTTP client to use for sending logs to the HTTPS log servers
	Client *http.Client
	// Context will be added to every log message
	Context logrus.Fields
	// ForceFlushField specifies the name of the log field triggering a force flush if present (default "logzruzForceFlush")
	ForceFlushField string
	// ForceFlushLevel allows for log messages higher than the level specified to be flushed instantly (default "fatal")
	ForceFlushLevel logrus.Level
	// Token stores the API token to authenticate to logz.io with
	Token string
	// URL allows users to overwrite the logz.io URL to send logs to. Should include protocol and port (default "https://listener.logz.io:8071/")
	URL string
	// UserAgent allows users to overwrite the default HTTP user-agent set for every request (default "logzruz vX.Y.Z - github.com/MorpheusXAUT/logzruz")
	UserAgent string
}

// NewHook creates a new logzruz hook using the provided options
func NewHook(options HookOptions) (*Hook, error) {
	if len(options.Token) == 0 {
		return nil, errors.NotValidf("invalid logz.io token")
	}

	if options.BufferCount < 0 {
		options.BufferCount = 0
	} else if options.BufferCount == 0 {
		options.BufferCount = DefaultBufferCount
	}
	if options.BufferInterval.Seconds() <= 0 {
		options.BufferInterval = DefaultBufferInterval
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

// Fire is triggered by logrus on every message passed to the hook.
// The log entry is processed, additional context and data added as required and the JSON encoded payload is added to the message buffer.
// If the buffer count is reached or a flush is force via log-field or level, the bulk posting is initiated.
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

// Levels returns the levels this hook is triggered on.
// As of now, logzruz will log every message to logz.io servers.
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

// Flush allows applications to force a buffer flash, sending all stored messages to logz.io
func (hook *Hook) Flush() error {
	err := hook.flushBuffer()
	if err != nil {
		return errors.Annotate(err, "failed to manually flush log buffer")
	}

	return nil
}

// Shutdown cleanly shuts the hook buffer loop down and flushes all remaining messages
func (hook *Hook) Shutdown() error {
	hook.bufferTicker.Stop()
	hook.stop <- true

	return hook.Flush()
}

// flushBuffer combines all buffered messages and sends them to the logz.io bulk POST endpoint
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

// bufferTickerLoop regularly triggers log flushing so messages do not get lost during low-message periods
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
