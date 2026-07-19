package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	sleep := flag.Duration("sleep", 0, "time to sleep")
	exit := flag.Int("exit", 0, "exit code")
	marker := flag.String("marker", "", "marker file")
	flag.Parse()
	if *marker != "" {
		_ = os.WriteFile(*marker, []byte(fmt.Sprintf("pid=%d\n", os.Getpid())), 0o600)
	}
	if *sleep > 0 {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		select {
		case <-time.After(*sleep):
		case <-signals:
			os.Exit(143)
		}
	}
	os.Exit(*exit)
}
