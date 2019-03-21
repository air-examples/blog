package main

import (
	"context"
	"errors"
	"fmt"
	stdLog "log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/air-examples/blog/handlers"
	"github.com/air-gases/defibrillator"
	"github.com/air-gases/limiter"
	"github.com/air-gases/logger"
	"github.com/aofei/air"
	"github.com/rs/zerolog/log"
)

var a = air.Default

func main() {
	a.ErrorHandler = errorHandler
	a.ErrorLogger = stdLog.New(&errorLogWriter{}, "", 0)

	a.Pregases = []air.Gas{
		logger.Gas(logger.GasConfig{}),
		defibrillator.Gas(defibrillator.GasConfig{}),
		limiter.BodySizeGas(limiter.BodySizeGasConfig{
			MaxBytes: 1 << 20,
		}),
	}

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := a.Serve(); err != nil {
			log.Error().Err(err).
				Str("app_name", a.AppName).
				Msg("server error")
		}
	}()

	<-shutdownChan
	ctx, _ := context.WithTimeout(context.Background(), time.Minute)
	a.Shutdown(ctx)
}

func errorHandler(err error, req *air.Request, res *air.Response) {
	if res.Written {
		return
	}

	m := ""
	if !req.Air.DebugMode && res.Status == http.StatusInternalServerError {
		m = http.StatusText(res.Status)
	} else {
		m = err.Error()
	}

	res.Render(map[string]interface{}{
		"PageTitle": fmt.Sprintf(
			"%s %d",
			req.LocalizedString("Error"),
			res.Status,
		),
		"Error": map[string]interface{}{
			"Code":    res.Status,
			"Message": m,
		},
	}, "error.html", "layouts/default.html")
}

type errorLogWriter struct{}

func (elw *errorLogWriter) Write(b []byte) (int, error) {
	log.Error().Err(errors.New(strings.TrimSuffix(string(b), "\n"))).
		Str("app_name", a.AppName).
		Msg("air error")

	return len(b), nil
}
