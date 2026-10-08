package main

import (
	"fmt"
	"log"

	"filosophy/shared"
)

func main() {
	report, err := shared.RunBenchmark()
	if err != nil {
		log.Fatalf("RunBenchmark failed: %v\nTo run the evaluation, ensure 'text/model.safetensors' is present.", err)
	}

	shared.PrintBenchmarkReport(func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	}, report)
}
