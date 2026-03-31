package main

import (
	"fmt"
	"os"

	toml "github.com/pelletier/go-toml/v2"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: validate_toml.go <path>")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var out map[string]any
	if err := toml.Unmarshal(data, &out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
