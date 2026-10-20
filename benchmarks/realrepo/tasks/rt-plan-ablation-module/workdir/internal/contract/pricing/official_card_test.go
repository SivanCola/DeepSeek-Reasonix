package pricing

import "testing"

func TestIsOfficialCardRecognisesEveryGenerationWeShipped(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model string
		card  RateCard
		want  bool
	}{
		{"current base", "deepseek-flash", RateCard{CacheHit: 0.003, Input: 0.15, Output: 0.6, Currency: "USD"}, true},
		{"current peak", "deepseek-flash", RateCard{CacheHit: 0.006, Input: 0.3, Output: 1.2, Currency: "USD"}, true},
		{"superseded base", "deepseek-flash", RateCard{CacheHit: 0.0028, Input: 0.14, Output: 0.28, Currency: "USD"}, true},
		{"superseded peak", "deepseek-flash", RateCard{CacheHit: 0.014, Input: 0.44, Output: 1.32, Currency: "USD"}, true},
		{"retired name prices as flash", "deepseek-v4-flash", RateCard{CacheHit: 0.003, Input: 0.15, Output: 0.6, Currency: "USD"}, true},
		{"cny card", "deepseek-flash", RateCard{CacheHit: 0.02, Input: 1, Output: 4, Currency: "CNY"}, true},
		{"peak numbers with the cached price missing", "deepseek-flash", RateCard{CacheHit: 0, Input: 0.3, Output: 1.2, Currency: "USD"}, false},
		{"cny numbers labelled usd", "deepseek-flash", RateCard{CacheHit: 0.02, Input: 1, Output: 4, Currency: "USD"}, false},
		{"a model the book does not price", "deepseek-unknown", RateCard{CacheHit: 0.003, Input: 0.15, Output: 0.6, Currency: "USD"}, false},
	} {
		if got := IsOfficialCard("deepseek", tc.model, tc.card); got != tc.want {
			t.Errorf("%s: IsOfficialCard = %v, want %v", tc.name, got, tc.want)
		}
	}
	if IsOfficialCard("someone-else", "deepseek-flash", RateCard{CacheHit: 0.003, Input: 0.15, Output: 0.6, Currency: "USD"}) {
		t.Error("another vendor's name matched DeepSeek's table")
	}
}
