package main

import (
	"context"
	"os"
)

func main() {
	os.Exit(Dispatch(context.Background(), os.Args[1:]))
}
