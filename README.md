logzruz
=========

[![GoDoc](https://godoc.org/github.com/MorpheusXAUT/logzruz?status.svg)](https://godoc.org/github.com/MorpheusXAUT/logzruz)

[Logrus](https://github.com/Sirupsen/logrus) hook for sending logs to [logz.io](http://logz.io/).

Package logzruz provides a logrus hook for sending log messages to logz.io servers for further processing.

The library buffers messages until a user-defined threshold has been reached or a ticker triggers a regular buffer flush.
By employing two methods of buffer flushing as well as providing a manual way to send messages, applications can utilise faster logging while making sure messages get sent within a reasonable time frame.

As of now, logzruz will send all message levels to logz.io

Installation
------

```bash
go get -u github.com/MorpheusXAUT/logzruz
```

Usage
------

logzruz can be added to logrus just like every other hook.
Applications are only required to provide an API token for the hook to function:

```go
package main

import (
    "github.com/Sirupsen/logrus"
	"github.com/MorpheusXAUT/logzruz"
)

func main() {
	logz, err := logzruz.NewHook(logzruz.HookOptions{
	    Token: "YOURLOGZIOTOKEN",
	}
	// check for token or URL parsing error
	if err != nil {
	    panic(err)
	}

	log.Hooks.Add(logz)

	log.Info("Hello to logz.io!")
}
```

Documentation
------
see https://godoc.org/github.com/MorpheusXAUT/logzruz

Attribution
------

### logrus
Structured, pluggable logging library developed by [Sirupsen](https://github.com/Sirupsen/logrus).

License
------

[MIT License](https://opensource.org/licenses/mit-license.php)