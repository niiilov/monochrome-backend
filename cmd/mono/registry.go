package main

import (
	"context"
	"fmt"
	"os"
	"sort"
)

type command struct {
	name  string
	short string
	fn    func(ctx context.Context, args []string) int
}

var commands []command

// Register adds a CLI command. Call it from init() of cmd_<name>.go.
func Register(name, short string, fn func(ctx context.Context, args []string) int) {
	commands = append(commands, command{name: name, short: short, fn: fn})
}

// Dispatch runs the named command or prints help.
func Dispatch(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printHelp()
		return 0
	}
	for _, c := range commands {
		if c.name == args[0] {
			return c.fn(ctx, args[1:])
		}
	}
	fmt.Fprintf(os.Stderr, "неизвестная команда: %s\n\n", args[0])
	printHelp()
	return 2
}

func printHelp() {
	fmt.Println("mono - подготовка новостного дайджеста по кибербезопасности")
	fmt.Println()
	fmt.Println("Использование: mono <команда> [аргументы]")
	fmt.Println()
	fmt.Println("Команды:")
	list := append([]command(nil), commands...)
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	for _, c := range list {
		fmt.Printf("  %-16s %s\n", c.name, c.short)
	}
}
