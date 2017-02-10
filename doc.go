// Package logzruz provides a logrus hook for sending log messages to logz.io servers for further processing.
//
// The library buffers messages until a user-defined threshold has been reached or a ticker triggers a regular buffer flush.
// By employing two methods of buffer flushing as well as providing a manual way to send messages, applications can utilise faster logging while making sure messages get sent within a reasonable time frame.
//
// As of now, logzruz will send all message levels to logz.io
package logzruz
