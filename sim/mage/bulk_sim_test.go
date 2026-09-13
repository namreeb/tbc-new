//go:build with_db

package mage

import (
	"testing"

	"github.com/wowsims/tbc/sim/core"
	"github.com/wowsims/tbc/sim/core/proto"
	googleProto "google.golang.org/protobuf/proto"
)

// Runs the batch sim end to end on a real Mage with two chest items in the
// pool: three combinations (equipped chest plus the two), gemmed with the
// fallback gems, constrained on fire resistance, then simmed.
func TestBulkSimEndToEnd(t *testing.T) {
	const (
		infernoweaveRobe  = 30762 // +60 fire resistance
		flameheartVest    = 30839 // +50 fire resistance
		runedLivingRuby   = 24030 // red gem: +9 spell damage
		gleamingDawnstone = 24050 // yellow gem: +8 spell crit rating
		greatDawnstone    = 31861 // yellow gem: +8 spell hit rating
		potentNobleTopaz  = 24059 // orange gem: +5 healing, +5 spell damage, +4 spell crit rating
		glowingNightseye  = 24056 // purple gem: +6 stamina, +5 healing, +5 spell damage
	)

	player := core.WithSpec(&proto.Player{
		Class:          proto.Class_ClassMage,
		Race:           proto.Race_RaceTroll,
		Equipment:      core.GetGearSet("../../ui/mage/dps/gear_sets", "p1Arcane").GearSet,
		Consumables:    &proto.ConsumesSpec{},
		Buffs:          core.FullIndividualBuffs,
		TalentsString:  "2500052300030150330125--053500031003001",
		Profession1:    proto.Profession_Engineering,
		Rotation:       core.GetAplRotation("../../ui/mage/dps/apls", "arcane").Rotation,
		ReactionTimeMs: 100,
	}, &proto.Player_Mage{Mage: &proto.Mage{Options: &proto.Mage_Options{ClassOptions: &proto.MageOptions{DefaultMageArmor: proto.MageArmor_MageArmorMageArmor}}}})

	base := &proto.RaidSimRequest{
		Raid:       core.SinglePlayerRaidProto(player, core.FullPartyBuffs, core.FullRaidBuffs, core.FullDebuffs),
		Encounter:  core.MakeDefaultEncounterCombos()[0].Encounter,
		SimOptions: &proto.SimOptions{Iterations: 50, RandomSeed: 1},
	}
	request := &proto.BulkSimRequest{
		Base: base,
		Settings: &proto.BulkSettings{
			IterationsPerCombo: 50,
		},
		Pool: []*proto.BulkPoolItem{
			{Item: &proto.ItemSpec{Id: infernoweaveRobe}, Slots: []proto.ItemSlot{proto.ItemSlot_ItemSlotChest}},
			{Item: &proto.ItemSpec{Id: flameheartVest}, Slots: []proto.ItemSlot{proto.ItemSlot_ItemSlotChest}},
		},
		TopN: 5,
	}

	if count := core.BulkSimCount(request); count.ErrorResult != "" || count.Combinations != 3 {
		t.Fatalf("BulkSimCount: %+v", count)
	}

	result := core.BulkSim(request)
	if result.Error != nil {
		t.Fatalf("unconstrained batch failed: %s", result.Error.Message)
	}
	if result.TotalCombinations != 3 || result.SkippedByConstraints != 0 {
		t.Fatalf("unconstrained batch: total %d skipped %d", result.TotalCombinations, result.SkippedByConstraints)
	}
	if result.Base == nil || result.Base.Dps == nil || result.Base.Dps.Avg <= 0 {
		t.Fatalf("base result missing or zero dps: %+v", result.Base)
	}
	// Without fallback gems the equipped-chest candidate is the base gear itself
	// and is excluded from the ranking, as in the browser.
	if len(result.Results) != 2 {
		t.Fatalf("expected 2 ranked results, got %d", len(result.Results))
	}
	if result.Results[0].Dps.Avg < result.Results[1].Dps.Avg {
		t.Fatalf("results must be sorted best first: %v then %v", result.Results[0].Dps.Avg, result.Results[1].Dps.Avg)
	}
	for _, combo := range result.Results {
		chest := combo.Equipment.Items[proto.ItemSlot_ItemSlotChest]
		if chest.Id != infernoweaveRobe && chest.Id != flameheartVest {
			t.Fatalf("ranked result wears an unexpected chest: %d", chest.Id)
		}
		if combo.Dps.Hist != nil || combo.Dps.AllValues != nil {
			t.Fatal("per-iteration data must be stripped from results")
		}
	}
	if result.Timings == nil || result.Timings.TotalMs <= 0 || result.Timings.SimsMs <= 0 {
		t.Fatalf("timings missing: %+v", result.Timings)
	}

	// With a fallback red gem, every candidate is regemmed, including the one
	// wearing the equipped chest, which then differs from the base gear and
	// ranks alongside the others (browser behaviour).
	request.Settings.DefaultRedGem = runedLivingRuby
	result = core.BulkSim(request)
	if result.Error != nil {
		t.Fatalf("fallback-gem batch failed: %s", result.Error.Message)
	}
	if len(result.Results) != 3 {
		t.Fatalf("expected 3 ranked results with fallback gems, got %d", len(result.Results))
	}
	for _, combo := range result.Results {
		chest := combo.Equipment.Items[proto.ItemSlot_ItemSlotChest]
		item, _ := core.LookupItem(chest.Id)
		for socketIdx, color := range item.GemSockets {
			if color == proto.GemColor_GemColorRed && (socketIdx >= len(chest.Gems) || chest.Gems[socketIdx] != runedLivingRuby) {
				t.Fatalf("chest %d socket %d should hold the fallback red gem, got %v", chest.Id, socketIdx, chest.Gems)
			}
		}
	}

	// Constrain on fire resistance just above the base character's value plus
	// the smaller chest's bonus: only the +60 robe passes.
	stats := core.ComputeStats(&proto.ComputeStatsRequest{Raid: base.Raid, Encounter: base.Encounter})
	baseFireRes := stats.RaidStats.Parties[0].Players[0].FinalStats.Stats[proto.Stat_StatFireResistance]
	request.Settings.StatConstraints = []*proto.BulkStatConstraint{{
		UnitStat: &proto.BulkStatConstraint_Stat{Stat: proto.Stat_StatFireResistance},
		Op:       proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThan,
		Value:    baseFireRes + 55,
	}}
	result = core.BulkSim(request)
	if result.Error != nil {
		t.Fatalf("constrained batch failed: %s", result.Error.Message)
	}
	if result.SkippedByConstraints != 2 || len(result.Results) != 1 {
		t.Fatalf("constrained batch: skipped %d, results %d (want 2 and 1)", result.SkippedByConstraints, len(result.Results))
	}
	if got := result.Results[0].Equipment.Items[proto.ItemSlot_ItemSlotChest].Id; got != infernoweaveRobe {
		t.Fatalf("only the +60 robe should survive, got %d", got)
	}

	// Gem optimization on: with spell damage worth the most, every red socket
	// gets the Runed Living Ruby and every socket ends up filled.
	request.Settings.StatConstraints = nil
	request.Settings.DefaultRedGem = 0
	weights := &proto.UnitStats{Stats: make([]float64, int(proto.Stat_StatPhysicalDamage)+1), PseudoStats: make([]float64, int(proto.PseudoStat_PseudoStatReducedCritTakenPercent)+1)}
	weights.Stats[proto.Stat_StatSpellDamage] = 1
	weights.Stats[proto.Stat_StatSpellHitRating] = 0.6
	weights.Stats[proto.Stat_StatSpellCritRating] = 0.5
	weights.Stats[proto.Stat_StatStamina] = 0.01
	request.Gems = &proto.BulkGemSettings{
		Optimize:       true,
		StatWeights:    weights,
		StatCaps:       &proto.UnitStats{Stats: make([]float64, len(weights.Stats)), PseudoStats: make([]float64, len(weights.PseudoStats))},
		UndershootCaps: &proto.UnitStats{Stats: make([]float64, len(weights.Stats)), PseudoStats: make([]float64, len(weights.PseudoStats))},
		EligibleGems: []*proto.BulkGemOption{
			{GemId: runedLivingRuby}, {GemId: gleamingDawnstone}, {GemId: greatDawnstone}, {GemId: potentNobleTopaz}, {GemId: glowingNightseye},
		},
	}
	result = core.BulkSim(request)
	if result.Error != nil {
		t.Fatalf("optimized batch failed: %s", result.Error.Message)
	}
	if len(result.Results) == 0 {
		t.Fatal("optimized batch produced no results")
	}
	for _, combo := range result.Results {
		for slot, spec := range combo.Equipment.Items {
			if spec.Id == 0 {
				continue
			}
			item, _ := core.LookupItem(spec.Id)
			for socketIdx, color := range item.GemSockets {
				if color == proto.GemColor_GemColorMeta {
					continue
				}
				if socketIdx >= len(spec.Gems) || spec.Gems[socketIdx] == 0 {
					t.Fatalf("slot %d item %d socket %d left empty after optimization: %v", slot, spec.Id, socketIdx, spec.Gems)
				}
				if color == proto.GemColor_GemColorRed && spec.Gems[socketIdx] != runedLivingRuby {
					t.Fatalf("slot %d red socket should hold Runed Living Ruby, got %d", slot, spec.Gems[socketIdx])
				}
			}
		}
	}
	if result.Timings.GemsMs <= 0 {
		t.Fatalf("gem phase should take measurable time, got %v", result.Timings.GemsMs)
	}

	// Split across workers, as the browser does, the pieces merge back into
	// exactly the unsplit result: same gear in the same order with the same
	// DPS, since seeds are keyed on the global combination index.
	request.Settings.DefaultRedGem = runedLivingRuby
	whole := core.BulkSim(request)
	if whole.Error != nil {
		t.Fatalf("unsplit batch failed: %s", whole.Error.Message)
	}
	split := core.SplitBulkSimRequest(request, 2)
	if split.ErrorResult != "" || split.SplitsDone != 2 {
		t.Fatalf("split: %+v", split)
	}
	var pieces []*proto.BulkSimResult
	for i, piece := range split.Requests {
		pieceResult := core.BulkSim(piece)
		if pieceResult.Error != nil {
			t.Fatalf("piece %d failed: %s", i, pieceResult.Error.Message)
		}
		if (pieceResult.Base != nil) != (i == 0) {
			t.Fatalf("piece %d base presence wrong: %v", i, pieceResult.Base != nil)
		}
		pieces = append(pieces, pieceResult)
	}
	// Merge in reverse order too: the outcome must not depend on which piece finishes first.
	merged := core.CombineBulkSimResults([]*proto.BulkSimResult{pieces[1], pieces[0]}, request.TopN)
	if merged.Error != nil {
		t.Fatalf("combine failed: %s", merged.Error.Message)
	}
	if merged.TotalCombinations != whole.TotalCombinations || merged.SkippedByConstraints != whole.SkippedByConstraints {
		t.Fatalf("merged totals %d/%d, want %d/%d", merged.TotalCombinations, merged.SkippedByConstraints, whole.TotalCombinations, whole.SkippedByConstraints)
	}
	if merged.Base.Dps.Avg != whole.Base.Dps.Avg {
		t.Fatalf("merged base dps %v, want %v", merged.Base.Dps.Avg, whole.Base.Dps.Avg)
	}
	if len(merged.Results) != len(whole.Results) {
		t.Fatalf("merged %d results, want %d", len(merged.Results), len(whole.Results))
	}
	for i := range whole.Results {
		if !googleProto.Equal(merged.Results[i].Equipment, whole.Results[i].Equipment) || merged.Results[i].Dps.Avg != whole.Results[i].Dps.Avg {
			t.Fatalf("merged result %d differs from the unsplit run: %v vs %v", i, merged.Results[i].Dps.Avg, whole.Results[i].Dps.Avg)
		}
	}
}
