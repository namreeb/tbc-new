package core

import (
	"testing"

	"github.com/wowsims/tbc/sim/core/proto"
	"github.com/wowsims/tbc/sim/core/stats"
)

const (
	gemTestBase     = int32(810_000_000)
	gemRedSpellDmg  = gemTestBase + 1 // red: +9 spell damage
	gemYellowHit    = gemTestBase + 2 // yellow: +8 spell hit rating
	gemOrangeMixed  = gemTestBase + 3 // orange: +5 spell damage, +4 spell crit rating
	gemPurpleUnique = gemTestBase + 4 // purple: +12 spell damage, unique
	gemBlueStam     = gemTestBase + 5 // blue: +12 stamina
	gemMetaTest     = gemTestBase + 6
	gemChestRedYel  = gemTestBase + 20 // chest: [Red, Yellow], bonus +5 spell damage
	gemHandsRed     = gemTestBase + 21 // hands: [Red], bonus +2 spell damage
	gemHeadMetaBlue = gemTestBase + 22 // head: [Meta, Blue], bonus +3 spell damage
)

func gemTestStats(values map[proto.Stat]float64) []float64 {
	arr := make([]float64, bulkNumStats)
	for stat, value := range values {
		arr[stat] = value
	}
	return arr
}

func gemRegisterTestDatabase() {
	sockets := func(colors ...proto.GemColor) bulkTestItemOpt {
		return func(item *proto.SimItem) { item.GemSockets = colors }
	}
	bonus := func(values map[proto.Stat]float64) bulkTestItemOpt {
		return func(item *proto.SimItem) { item.SocketBonus = gemTestStats(values) }
	}
	addToDatabase(&proto.SimDatabase{
		Items: []*proto.SimItem{
			bulkTestItem(gemChestRedYel, proto.ItemType_ItemTypeChest, sockets(proto.GemColor_GemColorRed, proto.GemColor_GemColorYellow), bonus(map[proto.Stat]float64{proto.Stat_StatSpellDamage: 5})),
			bulkTestItem(gemHandsRed, proto.ItemType_ItemTypeHands, sockets(proto.GemColor_GemColorRed), bonus(map[proto.Stat]float64{proto.Stat_StatSpellDamage: 2})),
			bulkTestItem(gemHeadMetaBlue, proto.ItemType_ItemTypeHead, sockets(proto.GemColor_GemColorMeta, proto.GemColor_GemColorBlue), bonus(map[proto.Stat]float64{proto.Stat_StatSpellDamage: 3})),
		},
		Gems: []*proto.SimGem{
			{Id: gemRedSpellDmg, Name: "Red SD", Color: proto.GemColor_GemColorRed, Stats: gemTestStats(map[proto.Stat]float64{proto.Stat_StatSpellDamage: 9})},
			{Id: gemYellowHit, Name: "Yellow Hit", Color: proto.GemColor_GemColorYellow, Stats: gemTestStats(map[proto.Stat]float64{proto.Stat_StatSpellHitRating: 8})},
			{Id: gemOrangeMixed, Name: "Orange", Color: proto.GemColor_GemColorOrange, Stats: gemTestStats(map[proto.Stat]float64{proto.Stat_StatSpellDamage: 5, proto.Stat_StatSpellCritRating: 4})},
			{Id: gemPurpleUnique, Name: "Purple Unique", Color: proto.GemColor_GemColorPurple, Stats: gemTestStats(map[proto.Stat]float64{proto.Stat_StatSpellDamage: 12}), Unique: true},
			{Id: gemBlueStam, Name: "Blue Stam", Color: proto.GemColor_GemColorBlue, Stats: gemTestStats(map[proto.Stat]float64{proto.Stat_StatStamina: 12})},
			{Id: gemMetaTest, Name: "Meta", Color: proto.GemColor_GemColorMeta},
		},
	})
}

// An optimizer with spell damage worth 1, spell hit rating 0.8, stamina 0.05.
func gemTestOptimizer(t *testing.T) *bulkGemOptimizer {
	t.Helper()
	gemRegisterTestDatabase()
	o := &bulkGemOptimizer{
		preCapEPs:      newBulkUnitStats(),
		statCaps:       newBulkUnitStats(),
		undershootCaps: newBulkUnitStats(),
		frozenSlots:    map[proto.ItemSlot]bool{},
		metaConditions: map[int32]*proto.BulkMetaGemCondition{},
		debuffStats:    newBulkUnitStats(),
	}
	o.preCapEPs.stats[proto.Stat_StatSpellDamage] = 1
	o.preCapEPs.stats[proto.Stat_StatSpellHitRating] = 0.8
	o.preCapEPs.stats[proto.Stat_StatStamina] = 0.05
	var eligible []bulkEligibleGem
	for _, id := range []int32{gemRedSpellDmg, gemYellowHit, gemOrangeMixed, gemPurpleUnique, gemBlueStam} {
		gem, _ := LookupGem(id)
		eligible = append(eligible, bulkEligibleGem{gem: gem})
	}
	o.setEligibleGems(eligible)
	return o
}

// Runs the optimizer on gear without going through ComputeStats, from given base stats.
func gemTestSolve(t *testing.T, o *bulkGemOptimizer, gear Equipment, baseStats bulkUnitStats) Equipment {
	t.Helper()
	cleared := o.clearGems(&gear)
	caps := o.capGaps(baseStats)
	softCaps := o.relativeSoftCaps(baseStats)
	weights := bulkCheckWeights(o.preCapEPs, caps, softCaps)
	model := o.buildModel(&cleared, o.gemOptions(weights, caps, softCaps), weights, caps, softCaps)
	solution, _, err := o.solveWithCaps(model, weights, caps, softCaps)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	return o.applySolution(cleared, &gear, model, solution)
}

func gemIDs(item Item) []int32 {
	ids := make([]int32, len(item.Gems))
	for i, gem := range item.Gems {
		ids[i] = gem.ID
	}
	return ids
}

func TestBulkGemApplyStatConversions(t *testing.T) {
	o := gemTestOptimizer(t)
	// A weighted stat stays a stat.
	c := newBulkCoeffs()
	o.applyStat(c, proto.Stat_StatSpellDamage, 9, o.preCapEPs)
	if c.stats[proto.Stat_StatSpellDamage] != 9 || len(c.pseudo) != 0 {
		t.Fatalf("weighted stat should be kept as-is: %+v", c)
	}
	// An unweighted rating with a weighted child becomes the child percentage.
	o.preCapEPs.stats[proto.Stat_StatSpellHitRating] = 0
	o.preCapEPs.pseudo[proto.PseudoStat_PseudoStatSpellHitPercent] = 10
	c = newBulkCoeffs()
	o.applyStat(c, proto.Stat_StatSpellHitRating, bulkSpellHitRatingPerPct, o.preCapEPs)
	if got := c.pseudo[proto.PseudoStat_PseudoStatSpellHitPercent]; got < 0.999 || got > 1.001 {
		t.Fatalf("hit rating should convert to 1%% spell hit, got %v", got)
	}
	if _, present := c.stats[proto.Stat_StatSpellHitRating]; present {
		t.Fatal("converted rating must not also stay as a stat")
	}
	// Racial multipliers.
	o.race = proto.Race_RaceHuman
	o.preCapEPs.stats[proto.Stat_StatSpirit] = 1
	c = newBulkCoeffs()
	o.applyStat(c, proto.Stat_StatSpirit, 10, o.preCapEPs)
	if got := c.stats[proto.Stat_StatSpirit]; got < 10.99 || got > 11.01 {
		t.Fatalf("human spirit should be scaled by 1.1, got %v", got)
	}
}

func TestBulkGemCheckWeights(t *testing.T) {
	weights := newBulkUnitStats()
	weights.stats[proto.Stat_StatSpellHitRating] = 0.8
	caps := newBulkUnitStats()
	caps.pseudo[proto.PseudoStat_PseudoStatSpellHitPercent] = 2 // a capped child
	validated := bulkCheckWeights(weights, caps, nil)
	if validated.stats[proto.Stat_StatSpellHitRating] != 0 {
		t.Fatal("parent rating weight should move to the capped child")
	}
	if got, want := validated.pseudo[proto.PseudoStat_PseudoStatSpellHitPercent], 0.8*bulkSpellHitRatingPerPct; got < want-1e-6 || got > want+1e-6 {
		t.Fatalf("child weight: got %v, want %v", got, want)
	}
	// A weighted child zeroes the parent.
	weights = newBulkUnitStats()
	weights.stats[proto.Stat_StatMeleeCritRating] = 1
	weights.pseudo[proto.PseudoStat_PseudoStatMeleeCritPercent] = 20
	validated = bulkCheckWeights(weights, newBulkUnitStats(), nil)
	if validated.stats[proto.Stat_StatMeleeCritRating] != 0 || validated.pseudo[proto.PseudoStat_PseudoStatMeleeCritPercent] != 20 {
		t.Fatalf("weighted child should zero the parent: %+v", validated)
	}
}

func TestBulkGemSolverPicksBestGemsAndBonus(t *testing.T) {
	o := gemTestOptimizer(t)
	var gear Equipment
	gear[proto.ItemSlot_ItemSlotChest] = NewItem(ItemSpec{ID: gemChestRedYel})
	result := gemTestSolve(t, o, gear, newBulkUnitStats())
	// Red socket: unique purple (12) beats red (9); yellow: hit gem (6.4) beats orange (5);
	// both match, so the +5 bonus is earned: 23.4 total.
	if got := gemIDs(result[proto.ItemSlot_ItemSlotChest]); got[0] != gemPurpleUnique || got[1] != gemYellowHit {
		t.Fatalf("chest gems: got %v, want [purple unique, yellow hit]", got)
	}
}

func TestBulkGemSolverUniqueGemUsedOnce(t *testing.T) {
	o := gemTestOptimizer(t)
	var gear Equipment
	gear[proto.ItemSlot_ItemSlotChest] = NewItem(ItemSpec{ID: gemChestRedYel})
	gear[proto.ItemSlot_ItemSlotHands] = NewItem(ItemSpec{ID: gemHandsRed})
	result := gemTestSolve(t, o, gear, newBulkUnitStats())
	purples := 0
	for _, slot := range []proto.ItemSlot{proto.ItemSlot_ItemSlotChest, proto.ItemSlot_ItemSlotHands} {
		for _, id := range gemIDs(result[slot]) {
			if id == gemPurpleUnique {
				purples++
			}
			if id == 0 {
				t.Fatalf("slot %d has an empty socket: %v", slot, gemIDs(result[slot]))
			}
		}
	}
	if purples != 1 {
		t.Fatalf("unique gem should be socketed exactly once, got %d", purples)
	}
	// The other red socket falls back to the plain red gem.
	reds := 0
	for _, slot := range []proto.ItemSlot{proto.ItemSlot_ItemSlotChest, proto.ItemSlot_ItemSlotHands} {
		if gemIDs(result[slot])[0] == gemRedSpellDmg {
			reds++
		}
	}
	if reds != 1 {
		t.Fatalf("expected one plain red gem in a red socket")
	}
}

func TestBulkGemSolverHardCaps(t *testing.T) {
	// Hit cap 4 rating above base: the +8 hit gem overshoots. As a lower bound
	// (default) the cap is enforced and hit's weight drops to zero, so the gem
	// stays only because nothing else satisfies >= 4; as an upper bound the
	// gem is excluded and the yellow socket takes the orange gem instead.
	o := gemTestOptimizer(t)
	o.statCaps.stats[proto.Stat_StatSpellHitRating] = 4
	var gear Equipment
	gear[proto.ItemSlot_ItemSlotChest] = NewItem(ItemSpec{ID: gemChestRedYel})
	result := gemTestSolve(t, o, gear, newBulkUnitStats())
	if got := gemIDs(result[proto.ItemSlot_ItemSlotChest])[1]; got != gemYellowHit {
		t.Fatalf("lower-bound cap: yellow socket should keep the hit gem to reach the cap, got %d", got)
	}

	o.undershootCaps.stats[proto.Stat_StatSpellHitRating] = 1
	result = gemTestSolve(t, o, gear, newBulkUnitStats())
	if got := gemIDs(result[proto.ItemSlot_ItemSlotChest])[1]; got != gemOrangeMixed {
		t.Fatalf("upper-bound cap: yellow socket should avoid the hit gem, got %d", got)
	}
}

func TestBulkGemSolverMetaCondition(t *testing.T) {
	o := gemTestOptimizer(t)
	o.metaConditions[gemMetaTest] = &proto.BulkMetaGemCondition{GemId: gemMetaTest, MinBlue: 1}
	var gear Equipment
	gear[proto.ItemSlot_ItemSlotHead] = NewItem(ItemSpec{ID: gemHeadMetaBlue, Gems: []int32{gemMetaTest, 0}})
	gear[proto.ItemSlot_ItemSlotChest] = NewItem(ItemSpec{ID: gemChestRedYel})
	result := gemTestSolve(t, o, gear, newBulkUnitStats())
	head := gemIDs(result[proto.ItemSlot_ItemSlotHead])
	if head[0] != gemMetaTest {
		t.Fatalf("meta gem must be kept, got %v", head)
	}
	// The purple unique counts as blue and is worth far more than the blue
	// stamina gem, so it lands in the head's blue socket to satisfy the meta.
	if head[1] != gemPurpleUnique {
		t.Fatalf("head blue socket should take the purple gem for the meta requirement, got %v", head)
	}
	if got := gemIDs(result[proto.ItemSlot_ItemSlotChest]); got[0] != gemRedSpellDmg {
		t.Fatalf("chest red socket should fall back to the plain red gem, got %v", got)
	}
	red, yellow, blue := bulkGemColorCounts(&result)
	if !bulkMetaGemConditionMet(o.metaConditions[gemMetaTest], red, yellow, blue) {
		t.Fatalf("meta condition should be met: r=%d y=%d b=%d", red, yellow, blue)
	}
}

func TestBulkGemSolverFrozenSlotKept(t *testing.T) {
	o := gemTestOptimizer(t)
	o.frozenSlots[proto.ItemSlot_ItemSlotHands] = true
	var gear Equipment
	gear[proto.ItemSlot_ItemSlotChest] = NewItem(ItemSpec{ID: gemChestRedYel})
	gear[proto.ItemSlot_ItemSlotHands] = NewItem(ItemSpec{ID: gemHandsRed, Gems: []int32{gemBlueStam}})
	result := gemTestSolve(t, o, gear, newBulkUnitStats())
	if got := gemIDs(result[proto.ItemSlot_ItemSlotHands]); got[0] != gemBlueStam {
		t.Fatalf("frozen slot gems must be untouched, got %v", got)
	}
}

func TestBulkGemMinimizeRegemsKeepsPlacement(t *testing.T) {
	// Two red sockets on different items, both originally gemmed; the solver
	// would be indifferent about which gets which gem, so the original
	// placement is preserved.
	o := gemTestOptimizer(t)
	var gear Equipment
	gear[proto.ItemSlot_ItemSlotChest] = NewItem(ItemSpec{ID: gemChestRedYel, Gems: []int32{gemRedSpellDmg, gemYellowHit}})
	gear[proto.ItemSlot_ItemSlotHands] = NewItem(ItemSpec{ID: gemHandsRed, Gems: []int32{gemPurpleUnique}})
	result := gemTestSolve(t, o, gear, newBulkUnitStats())
	if got := gemIDs(result[proto.ItemSlot_ItemSlotHands]); got[0] != gemPurpleUnique {
		t.Fatalf("purple gem should stay where it was, got %v", got)
	}
	if got := gemIDs(result[proto.ItemSlot_ItemSlotChest]); got[0] != gemRedSpellDmg || got[1] != gemYellowHit {
		t.Fatalf("chest gems should stay where they were, got %v", got)
	}
	_ = stats.Stats{}
}
