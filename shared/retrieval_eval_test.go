package shared

import (
	"testing"
)

// TestRetrievalEvaluation runs the 30-query retrieval benchmark and prints the report.
func TestRetrievalEvaluation(t *testing.T) {
	report, err := RunBenchmark()
	if err != nil {
		t.Fatalf("RunBenchmark failed: %v", err)
	}

	modes := []string{"Lexical (+ Fuzzy)", "Pure Semantic", "Filosophy Hybrid"}

	// Print benchmark report
	t.Log("\n=========================================================================================")
	t.Log("                     FILOSOPHY 30-QUERY RETRIEVAL EVALUATION REPORT                      ")
	t.Log("=========================================================================================")
	t.Logf("%-20s | %-10s | %-10s | %-10s | %-10s | %-10s | %-10s | %-12s",
		"Pipeline", "NDCG@10", "Recall@10", "MRR@10", "Mean (ms)", "p50 (ms)", "p95 (ms)", "Index-Only")
	t.Log("-------------------------------------------------------------------------------------------------------")
	for _, modeName := range modes {
		res := report.Results[modeName]
		t.Logf("%-20s | %-10.4f | %-10.4f | %-10.4f | %-10.2f | %-10.2f | %-10.2f | %-12.2f",
			res.Name, res.NDCG10, res.Recall10, res.MRR10, res.MeanLatMs, res.P50LatMs, res.P95LatMs, res.IndexOnlyLatMs)
	}
	t.Log("=========================================================================================")
	t.Log("                               CATEGORY BREAKDOWN (NDCG@10)                              ")
	t.Log("-----------------------------------------------------------------------------------------")
	t.Logf("%-35s | %-15s | %-15s | %-15s", "Category", "Lexical (+ Fuzzy)", "Pure Semantic", "Filosophy Hybrid")
	t.Log("-----------------------------------------------------------------------------------------")
	for _, cat := range report.SortedCategories {
		lex := report.CategoryMetrics[cat]["Lexical (+ Fuzzy)"][0]
		sem := report.CategoryMetrics[cat]["Pure Semantic"][0]
		hyb := report.CategoryMetrics[cat]["Filosophy Hybrid"][0]
		t.Logf("%-35s | %-15.4f | %-15.4f | %-15.4f", cat, lex, sem, hyb)
	}
	t.Log("=========================================================================================\n")

	lexRes := report.Results["Lexical (+ Fuzzy)"]
	semRes := report.Results["Pure Semantic"]
	hybRes := report.Results["Filosophy Hybrid"]

	if hybRes.NDCG10 <= lexRes.NDCG10 {
		t.Errorf("Hybrid NDCG@10 (%.4f) should exceed Lexical (%.4f)", hybRes.NDCG10, lexRes.NDCG10)
	}
	if hybRes.NDCG10 <= semRes.NDCG10 {
		t.Errorf("Hybrid NDCG@10 (%.4f) should exceed Semantic (%.4f)", hybRes.NDCG10, semRes.NDCG10)
	}
	if hybRes.Recall10 <= lexRes.Recall10 {
		t.Errorf("Hybrid Recall@10 (%.4f) should exceed Lexical (%.4f)", hybRes.Recall10, lexRes.Recall10)
	}
}
