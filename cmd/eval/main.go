package main

import (
	"fmt"
	"log"

	"filosophy/shared"
)

func main() {
	fmt.Println("=========================================================================================")
	fmt.Println("                     FILOSOPHY 30-QUERY RETRIEVAL BENCHMARK                              ")
	fmt.Println("=========================================================================================")

	report, err := shared.RunBenchmark()
	if err != nil {
		log.Fatalf("RunBenchmark failed: %v", err)
	}

	modes := []string{"Lexical (+ Fuzzy)", "Pure Semantic", "Filosophy Hybrid"}

	fmt.Printf("\n%-20s | %-10s | %-10s | %-10s | %-10s | %-10s | %-10s | %-12s\n",
		"Pipeline", "NDCG@10", "Recall@10", "MRR@10", "Mean (ms)", "p50 (ms)", "p95 (ms)", "Index-Only")
	fmt.Println("-------------------------------------------------------------------------------------------------------")
	for _, modeName := range modes {
		res := report.Results[modeName]
		fmt.Printf("%-20s | %-10.4f | %-10.4f | %-10.4f | %-10.2f | %-10.2f | %-10.2f | %-12.2f\n",
			res.Name, res.NDCG10, res.Recall10, res.MRR10, res.MeanLatMs, res.P50LatMs, res.P95LatMs, res.IndexOnlyLatMs)
	}

	fmt.Println("\n=========================================================================================")
	fmt.Println("                               CATEGORY BREAKDOWN (NDCG@10)                              ")
	fmt.Println("-----------------------------------------------------------------------------------------")
	fmt.Printf("%-35s | %-15s | %-15s | %-15s\n", "Category", "Lexical (+ Fuzzy)", "Pure Semantic", "Filosophy Hybrid")
	fmt.Println("-----------------------------------------------------------------------------------------")
	for _, cat := range report.SortedCategories {
		lex := report.CategoryMetrics[cat]["Lexical (+ Fuzzy)"][0]
		sem := report.CategoryMetrics[cat]["Pure Semantic"][0]
		hyb := report.CategoryMetrics[cat]["Filosophy Hybrid"][0]
		fmt.Printf("%-35s | %-15.4f | %-15.4f | %-15.4f\n", cat, lex, sem, hyb)
	}
	fmt.Println("=========================================================================================\n")
}
