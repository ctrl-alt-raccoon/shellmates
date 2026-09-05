package main

import (
	"context"
	"os"

	"github.com/ctrl-alt-raccoon/shellmates/internal/app"
)

var version = "dev"

func main() {
	os.Exit(app.Run(context.Background(), os.Args[0], os.Args[1:], app.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, version))
}
