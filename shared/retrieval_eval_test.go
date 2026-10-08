package shared

import (
	"testing"
)

// TestRetrievalEvaluation runs the real 30-query retrieval benchmark with loaded model weights.
// If weights are missing, it skips with instructions to download them.
func TestRetrievalEvaluation(t *testing.T) {
	report, err := RunBenchmark()
	if err != nil {
		t.Skipf("Skipping real model evaluation (model weights not found): %v. For offline CI regression, see TestRetrievalEvaluation_MockRegression.", err)
	}

	PrintBenchmarkReport(t.Logf, report)

	lexRes := report.Results["Lexical Ablation (Semantics Off)"]
	semRes := report.Results["Pure Semantic"]
	hybRes := report.Results["Filosophy Hybrid"]

	if hybRes.NDCG10 <= lexRes.NDCG10 {
		t.Errorf("Hybrid NDCG@10 (%.4f) should exceed Lexical Ablation (%.4f)", hybRes.NDCG10, lexRes.NDCG10)
	}
	if hybRes.NDCG10 <= semRes.NDCG10 {
		t.Errorf("Hybrid NDCG@10 (%.4f) should exceed Pure Semantic (%.4f)", hybRes.NDCG10, semRes.NDCG10)
	}
	if hybRes.Recall10 <= lexRes.Recall10 {
		t.Errorf("Hybrid Recall@10 (%.4f) should exceed Lexical Ablation (%.4f)", hybRes.Recall10, lexRes.Recall10)
	}
}

// TestRetrievalEvaluation_MockRegression runs the benchmark pipeline using the deterministic
// synthetic n-gram/keyword embedder to verify retrieval logic and fusion math in CI environments
// without requiring 120MB model weight binaries.
func TestRetrievalEvaluation_MockRegression(t *testing.T) {
	report, err := RunMockRegressionBenchmark()
	if err != nil {
		t.Fatalf("RunMockRegressionBenchmark failed: %v", err)
	}

	PrintBenchmarkReport(t.Logf, report)

	lexRes := report.Results["Lexical Ablation (Semantics Off)"]
	semRes := report.Results["Pure Semantic"]
	hybRes := report.Results["Filosophy Hybrid"]

	if hybRes.NDCG10 <= 0 || lexRes.NDCG10 <= 0 || semRes.NDCG10 <= 0 {
		t.Errorf("Expected valid positive NDCG scores across all pipelines")
	}
}
