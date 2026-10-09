// This offline GUI-process fixture never opens a socket or touches services.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(90)
	}
	time.Sleep(650 * time.Millisecond)
	data, _ := json.Marshal(os.Args[1:])
	if err := os.WriteFile(os.Args[2]+".observed.json", data, 0600); err != nil {
		os.Exit(91)
	}
	if strings.Contains(os.Args[2], "failure") {
		fmt.Fprintln(os.Stderr, "Simulated installer error")
		os.Exit(7)
	}
	fmt.Fprintln(os.Stdout, "Simulated command completed")
}
