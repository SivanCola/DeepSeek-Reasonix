#!/usr/bin/env bash
set -e
export GOFLAGS=-mod=mod GOTOOLCHAIN=local
cat > internal/contract/pricing/zz_grader_test.go <<'GO'
package pricing

import (
	"testing"
	"time"
)

var (
	_ func(string, string, string) (RateCard, bool)      = CurrentRate
	_ func(CatalogKey) (CatalogEntry, bool)              = LookupCatalog
	_                                                    = CatalogKey{Provider: "", Model: "", Currency: "", BillingMode: ""}
	_ func(ModelRef, RateCard) bool                      = IsOfficialCard
	_ func(ModelRef, RateCard) (CatalogEntry, bool)      = MatchesCatalog
	_ func(ModelRef, RateCard, time.Time) (Tariff, bool) = LiveTariff
	_                                                    = ModelRef{Provider: "", Model: ""}
)

func TestGraderCurrentRateReportsPresence(t *testing.T) {
	got, ok := CurrentRate("mimo", "mimo-v2.5", "CNY")
	if !ok || got != (RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}) {
		t.Fatalf("CurrentRate(mimo-v2.5) = %+v, %v", got, ok)
	}
	if got, ok := CurrentRate("nobody", "nothing", "USD"); ok || got != (RateCard{}) {
		t.Fatalf("an unpriced model answered %+v, %v", got, ok)
	}
	retired, ok := CurrentRate("deepseek", RetiredDeepSeekFlashModels()[0], "USD")
	flash, _ := CurrentRate("deepseek", "deepseek-flash", "USD")
	if !ok || retired != flash {
		t.Fatalf("a retired flash name no longer bills at the flash rate: %+v vs %+v", retired, flash)
	}
}

func TestGraderLookupCatalogKeepsPaygFirst(t *testing.T) {
	e, ok := LookupCatalog(CatalogKey{Provider: " MiMo ", Model: "mimo-v2.5", Currency: "cny"})
	if !ok || e.BillingMode != BillingModePAYG || e.Input != 1 {
		t.Fatalf("payg lookup = %+v, %v", e, ok)
	}
	if _, ok := LookupCatalog(CatalogKey{Provider: "mimo", Model: "mimo-v2.5", Currency: "USD"}); ok {
		t.Fatal("a currency the vendor does not publish was found")
	}
}

func TestGraderModelRefLookupsKeepTheirAnswers(t *testing.T) {
	mimo := ModelRef{Provider: "mimo", Model: "mimo-v2.5"}
	card := RateCard{CacheHit: 0.02, Input: 1, Output: 2, Currency: "CNY"}
	if !IsOfficialCard(mimo, card) {
		t.Fatal("the official mimo-v2.5 card is no longer recognised")
	}
	if _, ok := MatchesCatalog(ModelRef{Provider: " MiMo ", Model: "mimo-v2.5"}, card); !ok {
		t.Fatal("MatchesCatalog stopped normalising the provider")
	}
	flash := ModelRef{Provider: "deepseek", Model: "deepseek-flash"}
	cny, _ := CurrentRate("deepseek", "deepseek-flash", "CNY")
	at := time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC)
	if tariff, ok := LiveTariff(flash, cny, at); !ok || !tariff.Peak {
		t.Fatalf("LiveTariff at a weekday peak = %+v, %v", tariff, ok)
	}
	if _, ok := LiveTariff(mimo, card, at); ok {
		t.Fatal("a vendor with one rate around the clock reported a tariff")
	}
}
GO
gofmt_out="$(gofmt -l .)"
[ -z "$gofmt_out" ] || { echo "gofmt needed: $gofmt_out"; exit 1; }
go vet ./...
go test ./... >/dev/null
skipped="$(grep -rlE 't\.Skip|t\.SkipNow' --include='*_test.go' internal cmd || true)"
[ -z "$skipped" ] || { echo "tests skipped in: $skipped"; exit 1; }
out="$(go run ./cmd/benchctl rate -provider mimo -model mimo-v2.5)"
case "$out" in *"cache-hit 0.02 input 1 output 2"*) ;; *) echo "benchctl rate: $out"; exit 1 ;; esac
out="$(go run ./cmd/benchctl tariff -currency CNY -at 2026-10-12T02:00:00Z)"
case "$out" in *"official card: true"*"peak=true until 2026-10-12T04:00:00Z"*) ;; *) echo "benchctl tariff: $out"; exit 1 ;; esac
missing=0
while read -r name; do
	[ -n "$name" ] || continue
	grep -rqE "^func $name\(" --include='*_test.go' . || { echo "test removed: $name"; missing=1; }
done <<'TESTS'
TestAGeminiRateStepIsDatedNotEdited
TestAddMoneyRejectsMixedCurrency
TestAddMoneySameCurrency
TestAggregateCoverageAcrossCalls
TestAggregateCoverageIgnoresArrivalOrder
TestAggregateExplicitDisplayFallsBackToSameOriginal
TestAggregateMixedCurrenciesProducesBuckets
TestAggregateNoUsageIsUnavailable
TestAmountRoundTrip
TestApplyIgnoresUnusableRecords
TestApplyIsOrderStable
TestApplyKeepsEdgesDistinctByKind
TestApplyUpsertsNodesByID
TestArmNameIsIndependentOfSpecOrder
TestArmNamesTheConfiguration
TestBM25ScoreRanksMatchingDocument
TestBuildQuoteBillsThePeakRateInsideTheWindow
TestBuildQuoteCustomPriceFallsBackWithoutFX
TestBuildQuoteIdentityAndOfficialTableOnly
TestBuildQuoteMissingUsageIsUnavailable
TestBuildQuoteNoPriceIsUnavailable
TestCatalogPricesHaveBeenCheckedRecently
TestCompleteNeverSurvivesALaterIncomplete
TestCostChargesTheCurrentCard
TestCurrentRequestRateForDeepSeekSearchAndFetch
TestCustomPriceIsNeverRescheduled
TestEveryCatalogProviderHasAnOfficialEndpoint
TestEveryGoogleRowNamesItsSource
TestEveryRateRowReachesTheCatalog
TestFoldCoverageContract
TestFoldCoverageIsOrderIndependent
TestFoldCoverageTreatsAnUnnamedStateAsNothing
TestFoldIndexScaleNamesItsOwnArm
TestFoldIndexScaleRatios
TestGoogleModelsAreBilledAtGoogleOnlyOnGoogleHosts
TestGraphSurvivesJSONRoundTrip
TestIsConnReset
TestIsConnResetExcludesDNS
TestIsNilDetectsTypedNilInterface
TestIsNilRejectsConcreteValues
TestIsOfficialCardRecognisesEveryGenerationWeShipped
TestKeepTopRelativeScoreKeepsTopAndDropsWeakTail
TestKnownZeroIsCompleteAndNoUsageIsNot
TestLedgerEntryKeepsIncompletenessAcrossBucketing
TestLedgerMixedOriginalBucketsContinueAccumulating
TestLiveTariffIsTheVendorsAndNeverTheUsers
TestMakeSnippetHandlesMultibyteBoundary
TestMigrateLegacyUsageIdentityOnly
TestMigrateLegacyWipedZeroDoesNotGuess
TestMimoV26RatesFollowTheVendorPage
TestNextChangeFollowsTheVendorsScheduleAcrossAWeekend
TestNextChangeOfAWindowThatNeverFlipsIsNone
TestNilWindowNeverPeaks
TestNormalizeCurrency
TestNormalizeFillsCoverageForAQuoteThatPredatesIt
TestNormalizeOldQuote
TestOfficialProviderForEndpoint
TestOfficialRateRowsAreUniquelyIdentified
TestOriginalCostMatchesLegacySemantics
TestParseAcceptsSpecFormsAndRejectsUnknownModules
TestPeakRatesAndWindowsComeInPairs
TestPeakWindowIsAboutTheInstantNotTheReader
TestPeakWindowReadsTheHolidayTable
TestPeakWindowReadsTheVendorsClock
TestQuoteNamesTheRateBandItWasBilledIn
TestRankV2FieldWeightsBeatBodyMentions
TestRankV2FindsCodeSymbolBySegment
TestRateHistoryHasNoRepeatedGenerations
TestRateNamesTheCurrentCard
TestRateRefusesAnUnpricedModel
TestRequestCostIsPerSuccessfulRequest
TestRequestQuoteLandsInTheLedgerUnderItsOwnSource
TestRequestRatesAreAppendOnlyAndSourced
TestRetiredDeepSeekNamesBillAtTheFlashRate
TestSplitReportsUnbalancedParenthesesByIdentity
TestSplitSeparatesOnCommasAndWhitespaceOutsideParentheses
TestStateVocabulary
TestStringRoundTripsThroughParse
TestSupersededRatesExcludeTheCurrentOne
TestTariffReadsTheVendorsSchedule
TestTextDoesNotUndercountCJK
TestTextRoundsASCIIUpAndKeepsEmptyFree
TestTheRateInForceIsNotListedAsSuperseded
TestTokensHandlesLatinAndCJK
TestTokensV2CodeSymbolsAndCJK
TestUnknownSubcommandIsAUsageError
TestZeroValueIsTheControlArm
TESTS
[ "$missing" = 0 ]
