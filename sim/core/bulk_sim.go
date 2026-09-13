package core

import (
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/wowsims/tbc/sim/core/proto"
	"github.com/wowsims/tbc/sim/core/simsignals"
	googleProto "google.golang.org/protobuf/proto"
)

// Batch (bulk) sim: enumerate gear combinations from a pool, gem each one,
// drop those failing a stat constraint, sim the rest and return the top
// results. The browser's batch tab, the CLI and the WASM build all call this.

const bulkDefaultTopN = 5

func bulkErrorResult(format string, args ...any) *proto.BulkSimResult {
	return &proto.BulkSimResult{Error: &proto.ErrorOutcome{Type: proto.ErrorOutcomeType_ErrorOutcomeError, Message: fmt.Sprintf(format, args...)}}
}

func bulkAbortedResult() *proto.BulkSimResult {
	return &proto.BulkSimResult{Error: &proto.ErrorOutcome{Type: proto.ErrorOutcomeType_ErrorOutcomeAborted}}
}

// The subject player of a batch request, with the request's database registered.
func bulkSubject(request *proto.BulkSimRequest) (*proto.Player, error) {
	if request.Base == nil || request.Base.Raid == nil || len(request.Base.Raid.Parties) == 0 || len(request.Base.Raid.Parties[0].Players) == 0 {
		return nil, fmt.Errorf("batch request has no player")
	}
	player := request.Base.Raid.Parties[0].Players[0]
	if player == nil || player.Equipment == nil {
		return nil, fmt.Errorf("batch request has no player equipment")
	}
	if request.Database != nil {
		addToDatabase(request.Database)
	}
	if player.Database != nil {
		addToDatabase(player.Database)
	}
	return player, nil
}

func bulkBaseEquipment(player *proto.Player) (equipment Equipment, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("resolving equipment: %v", r)
		}
	}()
	equipment = ProtoToEquipment(player.Equipment)
	for slot := range equipment {
		bulkNormalizeGems(&equipment[slot])
	}
	return equipment, nil
}

// BulkSimCount enumerates a request without running anything: the combination
// count for the UI, or the validation error the batch would fail with.
func BulkSimCount(request *proto.BulkSimRequest) *proto.BulkSimCountResult {
	player, err := bulkSubject(request)
	if err != nil {
		return &proto.BulkSimCountResult{ErrorResult: err.Error()}
	}
	base, err := bulkBaseEquipment(player)
	if err != nil {
		return &proto.BulkSimCountResult{ErrorResult: err.Error()}
	}
	plan, err := newBulkPlan(request, &base)
	if err != nil {
		return &proto.BulkSimCountResult{ErrorResult: err.Error()}
	}
	return &proto.BulkSimCountResult{Combinations: int32(plan.count)}
}

func BulkSim(request *proto.BulkSimRequest) *proto.BulkSimResult {
	return runBulkSim(request, nil, simsignals.CreateSignals())
}

func BulkSimAsync(request *proto.BulkSimRequest, progress chan *proto.ProgressMetrics, requestId string) {
	signals, err := simsignals.RegisterWithId(requestId)
	if err != nil {
		progress <- &proto.ProgressMetrics{FinalBulkResult: bulkErrorResult("Couldn't register for signal API: %s", err.Error())}
		return
	}
	go func() {
		defer simsignals.UnregisterId(requestId)
		result := runBulkSim(request, progress, signals)
		progress <- &proto.ProgressMetrics{FinalBulkResult: result}
	}()
}

type bulkCandidate struct {
	idx       int
	gear      Equipment
	equipment *proto.EquipmentSpec
}

func runBulkSim(request *proto.BulkSimRequest, progress chan *proto.ProgressMetrics, signals simsignals.Signals) (result *proto.BulkSimResult) {
	defer func() {
		if r := recover(); r != nil {
			result = bulkErrorResult("batch sim failed: %v", r)
		}
	}()
	startedAt := time.Now()
	timings := &proto.BulkSimTimings{}
	report := func(phase proto.BulkSimPhase, completed, total int) {
		if progress != nil {
			progress <- &proto.ProgressMetrics{BulkPhase: phase, CompletedSims: int32(completed), TotalSims: int32(total)}
		}
	}
	aborted := func() bool { return signals.Abort.IsTriggered() }

	player, err := bulkSubject(request)
	if err != nil {
		return bulkErrorResult("%s", err.Error())
	}
	base, err := bulkBaseEquipment(player)
	if err != nil {
		return bulkErrorResult("%s", err.Error())
	}
	settings := request.Settings
	if settings == nil {
		settings = &proto.BulkSettings{}
	}
	gemSettings := request.Gems
	if gemSettings == nil {
		gemSettings = &proto.BulkGemSettings{}
	}
	topN := int(request.TopN)
	if topN <= 0 {
		topN = bulkDefaultTopN
	}
	workers := runtime.NumCPU()

	// Phase 1: enumerate and build candidates.
	phaseStart := time.Now()
	plan, err := newBulkPlan(request, &base)
	if err != nil {
		return bulkErrorResult("%s", err.Error())
	}
	comboStart, comboEnd, err := bulkComboRange(request, plan.count)
	if err != nil {
		return bulkErrorResult("%s", err.Error())
	}
	rangeCount := comboEnd - comboStart
	report(proto.BulkSimPhase_BulkSimPhaseBuild, 0, rangeCount)
	fallbackGems := bulkFallbackGems(settings)
	metaConditions := map[int32]*proto.BulkMetaGemCondition{}
	for _, cond := range gemSettings.MetaGemConditions {
		metaConditions[cond.GemId] = cond
	}
	candidates := make([]*bulkCandidate, 0, rangeCount)
	for idx := comboStart; idx < comboEnd; idx++ {
		if aborted() {
			return bulkAbortedResult()
		}
		gear := plan.buildCandidate(&base, idx, fallbackGems, metaConditions)
		candidates = append(candidates, &bulkCandidate{idx: idx, gear: gear})
	}
	report(proto.BulkSimPhase_BulkSimPhaseBuild, rangeCount, rangeCount)
	timings.BuildMs = float64(time.Since(phaseStart).Microseconds()) / 1000

	// Phase 2: gems. With optimization off, the fallback gems stand.
	phaseStart = time.Now()
	if gemSettings.Optimize {
		if err := bulkOptimizeGems(request, player, candidates, workers, aborted, func(done int) { report(proto.BulkSimPhase_BulkSimPhaseGems, done, len(candidates)) }); err != nil {
			if aborted() {
				return bulkAbortedResult()
			}
			return bulkErrorResult("%s", err.Error())
		}
	}
	for _, candidate := range candidates {
		bulkApplyMetaGemState(&candidate.gear, metaConditions)
		candidate.equipment = candidate.gear.ToEquipmentSpecProto()
	}
	timings.GemsMs = float64(time.Since(phaseStart).Microseconds()) / 1000

	// Phase 3: constraints, against the same final stats the character sheet shows.
	phaseStart = time.Now()
	survivors := candidates
	if len(settings.StatConstraints) > 0 {
		passes := make([]bool, len(candidates))
		err := bulkRunParallel(len(candidates), workers, aborted, func(i int) error {
			finalStats, err := bulkFinalStats(request.Base, candidates[i].equipment)
			if err != nil {
				return err
			}
			passes[i] = bulkFinalStatsPassConstraints(settings.StatConstraints, finalStats)
			return nil
		}, func(done int) { report(proto.BulkSimPhase_BulkSimPhaseConstraints, done, len(candidates)) })
		if err != nil {
			if aborted() {
				return bulkAbortedResult()
			}
			return bulkErrorResult("%s", err.Error())
		}
		survivors = nil
		for i, candidate := range candidates {
			if passes[i] {
				survivors = append(survivors, candidate)
			}
		}
	}
	timings.ConstraintsMs = float64(time.Since(phaseStart).Microseconds()) / 1000

	// Phase 4: sims. The base gear first, for reference, then every survivor.
	phaseStart = time.Now()
	iterations := settings.IterationsPerCombo
	if iterations <= 0 && request.Base.SimOptions != nil {
		iterations = request.Base.SimOptions.Iterations
	}
	baseSpec := base.ToEquipmentSpecProto()
	var baseCombo *proto.BulkSimCombo
	baseSims := 0
	if comboStart == 0 {
		baseSims = 1
	}
	report(proto.BulkSimPhase_BulkSimPhaseSims, 0, len(survivors)+baseSims)
	if baseSims > 0 {
		baseDps, err := bulkSimGear(request.Base, baseSpec, iterations, 0)
		if err != nil {
			return bulkErrorResult("%s", err.Error())
		}
		baseCombo = &proto.BulkSimCombo{Equipment: baseSpec, Dps: baseDps}
	}
	results := make([]*proto.BulkSimCombo, len(survivors))
	err = bulkRunParallel(len(survivors), workers, aborted, func(i int) error {
		dps, err := bulkSimGear(request.Base, survivors[i].equipment, iterations, int64(survivors[i].idx)+1)
		if err != nil {
			return err
		}
		results[i] = &proto.BulkSimCombo{Equipment: survivors[i].equipment, Dps: dps}
		return nil
	}, func(done int) { report(proto.BulkSimPhase_BulkSimPhaseSims, done+baseSims, len(survivors)+baseSims) })
	if err != nil {
		if aborted() {
			return bulkAbortedResult()
		}
		return bulkErrorResult("%s", err.Error())
	}
	timings.SimsMs = float64(time.Since(phaseStart).Microseconds()) / 1000

	// Rank, dropping the candidate that is the base gear itself, as the UI does.
	var ranked []*proto.BulkSimCombo
	for i, combo := range results {
		if bulkSameGear(&survivors[i].gear, &base) {
			continue
		}
		ranked = append(ranked, combo)
	}
	ranked = bulkTopResults(ranked, topN)
	timings.TotalMs = float64(time.Since(startedAt).Microseconds()) / 1000

	return &proto.BulkSimResult{
		Results:              ranked,
		Base:                 baseCombo,
		TotalCombinations:    int32(plan.count),
		SkippedByConstraints: int32(len(candidates) - len(survivors)),
		Timings:              timings,
	}
}

// The [start, end) combination range a request covers; the whole batch when
// unset.
func bulkComboRange(request *proto.BulkSimRequest, count int) (int, int, error) {
	start, end := int(request.ComboStart), int(request.ComboEnd)
	if start == 0 && end == 0 {
		return 0, count, nil
	}
	if start < 0 || end < start || end > count {
		return 0, 0, fmt.Errorf("combination range [%d, %d) is outside the batch's %d combinations", start, end, count)
	}
	return start, end, nil
}

// Sorts by average DPS, best first, and keeps the top n.
func bulkTopResults(results []*proto.BulkSimCombo, n int) []*proto.BulkSimCombo {
	sort.SliceStable(results, func(a, b int) bool { return results[a].Dps.Avg > results[b].Dps.Avg })
	if len(results) > n {
		results = results[:n]
	}
	return results
}

// SplitBulkSimRequest divides a batch into at most splitCount pieces by
// combination range, one per worker. The batch is validated and counted once
// here so the pieces only carry a range. Pieces are as even as possible; with
// fewer combinations than splits, one piece per combination.
func SplitBulkSimRequest(request *proto.BulkSimRequest, splitCount int32) *proto.BulkSimRequestSplitResult {
	counted := BulkSimCount(request)
	if counted.ErrorResult != "" {
		return &proto.BulkSimRequestSplitResult{ErrorResult: counted.ErrorResult}
	}
	if request.ComboStart != 0 || request.ComboEnd != 0 {
		return &proto.BulkSimRequestSplitResult{ErrorResult: "cannot split a request that already has a combination range"}
	}
	count := int(counted.Combinations)
	splits := max(1, min(int(splitCount), count))
	result := &proto.BulkSimRequestSplitResult{SplitsDone: int32(splits)}
	for i := 0; i < splits; i++ {
		piece := googleProto.Clone(request).(*proto.BulkSimRequest)
		piece.ComboStart = int32(i * count / splits)
		piece.ComboEnd = int32((i + 1) * count / splits)
		result.Requests = append(result.Requests, piece)
	}
	return result
}

// CombineBulkSimResults merges the results of a split batch's pieces: the top
// n of all their results, the base from the piece that simmed it, skips summed
// and, since the pieces ran side by side, each phase's time is the slowest
// piece's. Any piece's error is the batch's error.
func CombineBulkSimResults(results []*proto.BulkSimResult, topN int32) *proto.BulkSimResult {
	if len(results) == 0 {
		return bulkErrorResult("no batch results to combine")
	}
	n := int(topN)
	if n <= 0 {
		n = bulkDefaultTopN
	}
	combined := &proto.BulkSimResult{Timings: &proto.BulkSimTimings{}}
	for _, piece := range results {
		if piece == nil {
			continue
		}
		if piece.Error != nil {
			return &proto.BulkSimResult{Error: piece.Error}
		}
		combined.Results = append(combined.Results, piece.Results...)
		if combined.Base == nil {
			combined.Base = piece.Base
		}
		combined.TotalCombinations = piece.TotalCombinations
		combined.SkippedByConstraints += piece.SkippedByConstraints
		if t := piece.Timings; t != nil {
			combined.Timings.BuildMs = max(combined.Timings.BuildMs, t.BuildMs)
			combined.Timings.GemsMs = max(combined.Timings.GemsMs, t.GemsMs)
			combined.Timings.ConstraintsMs = max(combined.Timings.ConstraintsMs, t.ConstraintsMs)
			combined.Timings.SimsMs = max(combined.Timings.SimsMs, t.SimsMs)
			combined.Timings.TotalMs = max(combined.Timings.TotalMs, t.TotalMs)
		}
	}
	if combined.Base == nil {
		return bulkErrorResult("no batch piece simmed the base gear")
	}
	combined.Results = bulkTopResults(combined.Results, n)
	return combined
}

// Runs fn(0..n-1) with at most `workers` in flight, stopping at the first error
// or abort. onProgress is called with the number completed so far.
func bulkRunParallel(n, workers int, aborted func() bool, fn func(i int) error, onProgress func(done int)) error {
	if n == 0 {
		return nil
	}
	workers = max(1, min(workers, n))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		done     int
		next     int
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if firstErr != nil || next >= n {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()
				if aborted() {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("aborted")
					}
					mu.Unlock()
					return
				}
				err := fn(i)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = err
				}
				done++
				completed := done
				mu.Unlock()
				if err == nil && onProgress != nil {
					onProgress(completed)
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// The request's raid with the subject wearing the given equipment.
func bulkRaidWithEquipment(base *proto.RaidSimRequest, equipment *proto.EquipmentSpec) *proto.Raid {
	raid := googleProto.Clone(base.Raid).(*proto.Raid)
	raid.Parties[0].Players[0].Equipment = equipment
	return raid
}

// Final stats of the subject wearing the equipment, as ComputeStats reports them.
func bulkFinalStats(base *proto.RaidSimRequest, equipment *proto.EquipmentSpec) (finalStats *proto.UnitStats, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("computing stats: %v", r)
		}
	}()
	result := ComputeStats(&proto.ComputeStatsRequest{Raid: bulkRaidWithEquipment(base, equipment), Encounter: base.Encounter})
	if result.ErrorResult != "" {
		return nil, fmt.Errorf("computing stats: %s", result.ErrorResult)
	}
	return result.RaidStats.Parties[0].Players[0].FinalStats, nil
}

// Sims the subject wearing the equipment, single-threaded, returning its DPS
// distribution without the per-iteration data.
func bulkSimGear(base *proto.RaidSimRequest, equipment *proto.EquipmentSpec, iterations int32, seedOffset int64) (dps *proto.DistributionMetrics, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("simming: %v", r)
		}
	}()
	request := googleProto.Clone(base).(*proto.RaidSimRequest)
	request.Raid = bulkRaidWithEquipment(base, equipment)
	if request.SimOptions == nil {
		request.SimOptions = &proto.SimOptions{}
	}
	request.SimOptions.Iterations = iterations
	request.SimOptions.Debug = false
	request.SimOptions.DebugFirstIteration = false
	request.SimOptions.SaveAllValues = false
	if request.SimOptions.RandomSeed != 0 {
		request.SimOptions.RandomSeed += seedOffset
	}
	result := RunRaidSim(request)
	if result.Error != nil {
		return nil, fmt.Errorf("simming: %s", result.Error.Message)
	}
	dps = result.RaidMetrics.Parties[0].Players[0].Dps
	dps.Hist = nil
	dps.AllValues = nil
	return dps, nil
}

// Gem optimization per candidate. Implemented in bulk_sim_gems.go; this stub
// keeps the fallback gems until that lands.
var bulkOptimizeGems = func(request *proto.BulkSimRequest, player *proto.Player, candidates []*bulkCandidate, workers int, aborted func() bool, onProgress func(done int)) error {
	onProgress(len(candidates))
	return nil
}
