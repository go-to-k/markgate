package main

import "github.com/go-to-k/markgate/internal/cli"

// version is overridden by GoReleaser via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	cli.Execute(version)
}
