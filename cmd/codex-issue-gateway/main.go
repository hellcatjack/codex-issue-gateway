package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	configPath := flag.String("config", "", "path to gateway config file")
	flag.Parse()
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "missing --config")
		os.Exit(2)
	}
	fmt.Printf("codex-issue-gateway config=%s\n", *configPath)
}
