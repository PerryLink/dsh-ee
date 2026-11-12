package main

import "io"

func cliConfig(args []string, stdout, stderr io.Writer) int { return 2 }
func cliAdmin(args []string, stdout, stderr io.Writer) int  { return 2 }
