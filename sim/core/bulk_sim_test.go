package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wowsims/tbc/sim/core/proto"
	googleProto "google.golang.org/protobuf/proto"
)

// Synthetic items for the batch sim tests, in an id range no real item uses.
const bulkTestBase = int32(800_000_000)

type bulkTestItemOpt func(*proto.SimItem)

func bulkTestItem(id int32, itemType proto.ItemType, opts ...bulkTestItemOpt) *proto.SimItem {
	item := &proto.SimItem{
		Id:             id,
		Name:           fmt.Sprintf("Bulk Test Item %d", id),
		Type:           itemType,
		ScalingOptions: map[int32]*proto.ScalingItemProperties{0: {Stats: map[int32]float64{}}},
	}
	for _, opt := range opts {
		opt(item)
	}
	return item
}

func withWeapon(handType proto.HandType, weaponType proto.WeaponType) bulkTestItemOpt {
	return func(item *proto.SimItem) {
		item.Type = proto.ItemType_ItemTypeWeapon
		item.HandType = handType
		item.WeaponType = weaponType
		item.WeaponSpeed = 2.0
	}
}

func withSockets(colors ...proto.GemColor) bulkTestItemOpt {
	return func(item *proto.SimItem) { item.GemSockets = colors }
}

func withUnique() bulkTestItemOpt { return func(item *proto.SimItem) { item.Unique = true } }
func withLimit(cat int32) bulkTestItemOpt {
	return func(item *proto.SimItem) { item.LimitCategory = cat }
}

var (
	bulkHead0, bulkHead1, bulkHead2        = bulkTestBase + 1, bulkTestBase + 2, bulkTestBase + 3
	bulkRing0, bulkRing1, bulkRingA        = bulkTestBase + 10, bulkTestBase + 11, bulkTestBase + 12
	bulkRingB, bulkRingUnique              = bulkTestBase + 13, bulkTestBase + 14
	bulkRingLimit1, bulkRingLimit2         = bulkTestBase + 15, bulkTestBase + 16
	bulkChest0, bulkChest1                 = bulkTestBase + 20, bulkTestBase + 21
	bulkSword0, bulkSwordX, bulkSwordY     = bulkTestBase + 30, bulkTestBase + 31, bulkTestBase + 32
	bulkTwoHander, bulkShield0, bulkShield = bulkTestBase + 33, bulkTestBase + 34, bulkTestBase + 35
	bulkGemRed, bulkGemBlue, bulkGemUnique = bulkTestBase + 40, bulkTestBase + 41, bulkTestBase + 42
	bulkGemMeta                            = bulkTestBase + 43
	bulkEnchantWeapon, bulkEnchantChest    = bulkTestBase + 50, bulkTestBase + 51
	bulkEnchantTwoHand                     = bulkTestBase + 52
)

func bulkRegisterTestDatabase() {
	addToDatabase(&proto.SimDatabase{
		Items: []*proto.SimItem{
			bulkTestItem(bulkHead0, proto.ItemType_ItemTypeHead, withSockets(proto.GemColor_GemColorMeta, proto.GemColor_GemColorRed)),
			bulkTestItem(bulkHead1, proto.ItemType_ItemTypeHead),
			bulkTestItem(bulkHead2, proto.ItemType_ItemTypeHead),
			bulkTestItem(bulkRing0, proto.ItemType_ItemTypeFinger),
			bulkTestItem(bulkRing1, proto.ItemType_ItemTypeFinger),
			bulkTestItem(bulkRingA, proto.ItemType_ItemTypeFinger),
			bulkTestItem(bulkRingB, proto.ItemType_ItemTypeFinger),
			bulkTestItem(bulkRingUnique, proto.ItemType_ItemTypeFinger, withUnique()),
			bulkTestItem(bulkRingLimit1, proto.ItemType_ItemTypeFinger, withLimit(7)),
			bulkTestItem(bulkRingLimit2, proto.ItemType_ItemTypeFinger, withLimit(7)),
			bulkTestItem(bulkChest0, proto.ItemType_ItemTypeChest, withSockets(proto.GemColor_GemColorRed, proto.GemColor_GemColorBlue)),
			bulkTestItem(bulkChest1, proto.ItemType_ItemTypeChest, withSockets(proto.GemColor_GemColorBlue, proto.GemColor_GemColorRed, proto.GemColor_GemColorYellow)),
			bulkTestItem(bulkSword0, 0, withWeapon(proto.HandType_HandTypeOneHand, proto.WeaponType_WeaponTypeSword)),
			bulkTestItem(bulkSwordX, 0, withWeapon(proto.HandType_HandTypeOneHand, proto.WeaponType_WeaponTypeSword)),
			bulkTestItem(bulkSwordY, 0, withWeapon(proto.HandType_HandTypeOneHand, proto.WeaponType_WeaponTypeSword)),
			bulkTestItem(bulkTwoHander, 0, withWeapon(proto.HandType_HandTypeTwoHand, proto.WeaponType_WeaponTypeSword)),
			bulkTestItem(bulkShield0, 0, withWeapon(proto.HandType_HandTypeOffHand, proto.WeaponType_WeaponTypeShield)),
			bulkTestItem(bulkShield, 0, withWeapon(proto.HandType_HandTypeOffHand, proto.WeaponType_WeaponTypeShield)),
		},
		Gems: []*proto.SimGem{
			{Id: bulkGemRed, Name: "Red", Color: proto.GemColor_GemColorRed},
			{Id: bulkGemBlue, Name: "Blue", Color: proto.GemColor_GemColorBlue},
			{Id: bulkGemUnique, Name: "Unique Red", Color: proto.GemColor_GemColorRed, Unique: true},
			{Id: bulkGemMeta, Name: "Meta", Color: proto.GemColor_GemColorMeta},
		},
		Enchants: []*proto.SimEnchant{
			{EffectId: bulkEnchantWeapon, Name: "Weapon Enchant", Type: proto.ItemType_ItemTypeWeapon},
			{EffectId: bulkEnchantChest, Name: "Chest Enchant", Type: proto.ItemType_ItemTypeChest},
			{EffectId: bulkEnchantTwoHand, Name: "Two-Hand Enchant", Type: proto.ItemType_ItemTypeWeapon, EnchantType: proto.EnchantType_EnchantTypeTwoHand},
		},
	})
}

func bulkSpec(id int32, gems ...int32) *proto.ItemSpec { return &proto.ItemSpec{Id: id, Gems: gems} }

func bulkTestRequest(equipment map[proto.ItemSlot]*proto.ItemSpec, pool []*proto.BulkPoolItem, canDualWield bool) *proto.BulkSimRequest {
	spec := &proto.EquipmentSpec{Items: make([]*proto.ItemSpec, NumItemSlots)}
	for slot := range spec.Items {
		spec.Items[slot] = &proto.ItemSpec{}
	}
	for slot, item := range equipment {
		spec.Items[slot] = item
	}
	return &proto.BulkSimRequest{
		Base:         &proto.RaidSimRequest{Raid: &proto.Raid{Parties: []*proto.Party{{Players: []*proto.Player{{Equipment: spec}}}}}},
		Settings:     &proto.BulkSettings{},
		Pool:         pool,
		CanDualWield: canDualWield,
	}
}

func poolItem(id int32, slots ...proto.ItemSlot) *proto.BulkPoolItem {
	return &proto.BulkPoolItem{Item: bulkSpec(id), Slots: slots}
}

func bulkPlanFor(t *testing.T, request *proto.BulkSimRequest) (*bulkPlan, Equipment) {
	t.Helper()
	bulkRegisterTestDatabase()
	player, err := bulkSubject(request)
	if err != nil {
		t.Fatal(err)
	}
	base, err := bulkBaseEquipment(player)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := newBulkPlan(request, &base)
	if err != nil {
		t.Fatal(err)
	}
	return plan, base
}

// Every combination index must decode to a distinct assignment.
func bulkAssertDistinctCombos(t *testing.T, plan *bulkPlan) {
	t.Helper()
	seen := map[string]bool{}
	for idx := 0; idx < plan.count; idx++ {
		var parts []string
		for _, a := range plan.combo(idx) {
			parts = append(parts, fmt.Sprintf("%d=%d", a.slot, a.option.item.ID))
		}
		key := strings.Join(parts, ",")
		if seen[key] {
			t.Fatalf("combination %d duplicates an earlier one: %s", idx, key)
		}
		seen[key] = true
	}
}

func TestBulkPlanNonDualWield(t *testing.T) {
	mh, oh, f1, f2 := proto.ItemSlot_ItemSlotMainHand, proto.ItemSlot_ItemSlotOffHand, proto.ItemSlot_ItemSlotFinger1, proto.ItemSlot_ItemSlotFinger2
	request := bulkTestRequest(map[proto.ItemSlot]*proto.ItemSpec{
		proto.ItemSlot_ItemSlotHead: bulkSpec(bulkHead0), f1: bulkSpec(bulkRing0), f2: bulkSpec(bulkRing1),
		mh: bulkSpec(bulkSword0), oh: bulkSpec(bulkShield0), proto.ItemSlot_ItemSlotChest: bulkSpec(bulkChest0),
	}, []*proto.BulkPoolItem{
		poolItem(bulkHead1, proto.ItemSlot_ItemSlotHead), poolItem(bulkHead2, proto.ItemSlot_ItemSlotHead),
		poolItem(bulkRingA, f1, f2), poolItem(bulkRingB, f1, f2),
		poolItem(bulkChest1, proto.ItemSlot_ItemSlotChest),
		poolItem(bulkSwordX, mh), poolItem(bulkTwoHander, mh), poolItem(bulkShield, oh),
	}, false)
	plan, _ := bulkPlanFor(t, request)

	// Weapons: the two-hander alone, plus {equipped sword, sword X} x {equipped shield, shield}.
	if got := len(plan.weaponCombos); got != 5 {
		t.Fatalf("weapon combos: got %d, want 5", got)
	}
	// Rings: 4 options -> 6 pairs. Heads: 3. Chests: 2.
	if got := len(plan.pairs[bulkSlotFinger]); got != 6 {
		t.Fatalf("ring pairs: got %d, want 6", got)
	}
	if plan.count != 5*6*3*2 {
		t.Fatalf("count: got %d, want %d", plan.count, 5*6*3*2)
	}
	bulkAssertDistinctCombos(t, plan)
	if got := BulkSimCount(request); got.ErrorResult != "" || int(got.Combinations) != plan.count {
		t.Fatalf("BulkSimCount: got %+v, want %d", got, plan.count)
	}
}

func TestBulkPlanDualWield(t *testing.T) {
	mh, oh := proto.ItemSlot_ItemSlotMainHand, proto.ItemSlot_ItemSlotOffHand
	request := bulkTestRequest(map[proto.ItemSlot]*proto.ItemSpec{
		mh: bulkSpec(bulkSword0), oh: bulkSpec(bulkSwordY),
	}, []*proto.BulkPoolItem{
		poolItem(bulkSwordX, mh, oh), poolItem(bulkSwordX, mh, oh), // two copies
		poolItem(bulkTwoHander, mh),
	}, true)
	plan, _ := bulkPlanFor(t, request)
	// One-handers {X, sword0, swordY}: 3 ordered pairs each way = 6, plus X in both hands, plus the two-hander.
	if got := len(plan.weaponCombos); got != 6+1+1 {
		t.Fatalf("dual wield weapon combos: got %d, want 8", got)
	}
	bulkAssertDistinctCombos(t, plan)
}

func TestBulkPlanFrozenRingAndUniqueRules(t *testing.T) {
	f1, f2 := proto.ItemSlot_ItemSlotFinger1, proto.ItemSlot_ItemSlotFinger2
	request := bulkTestRequest(map[proto.ItemSlot]*proto.ItemSpec{f1: bulkSpec(bulkRing0), f2: bulkSpec(bulkRing1)}, []*proto.BulkPoolItem{
		poolItem(bulkRingA, f1, f2), poolItem(bulkRingA, f1, f2), // two copies of A
		poolItem(bulkRingUnique, f1, f2), poolItem(bulkRingUnique, f1, f2), // second copy rejected
	}, false)
	plan, _ := bulkPlanFor(t, request)
	// Options {ring0, ring1, A, U}: 6 pairs + [A, A]; never [U, U].
	if got := len(plan.pairs[bulkSlotFinger]); got != 7 {
		t.Fatalf("ring pairs: got %d, want 7", got)
	}

	request.Settings.FreezeRingSlot = int32(f1)
	plan, _ = bulkPlanFor(t, request)
	// Frozen ring0 pairs with every other option: ring1, A, U.
	if got := len(plan.pairs[bulkSlotFinger]); got != 3 {
		t.Fatalf("frozen ring pairs: got %d, want 3", got)
	}
	for _, pair := range plan.pairs[bulkSlotFinger] {
		if pair[0].item.ID != bulkRing0 {
			t.Fatalf("frozen ring must be first in every pair, got %d", pair[0].item.ID)
		}
	}
}

func TestBulkPlanErrors(t *testing.T) {
	f1, f2 := proto.ItemSlot_ItemSlotFinger1, proto.ItemSlot_ItemSlotFinger2
	bulkRegisterTestDatabase()

	single := bulkTestRequest(nil, []*proto.BulkPoolItem{poolItem(bulkRingA, f1, f2)}, false)
	if got := BulkSimCount(single); !strings.Contains(got.ErrorResult, "At least 2 items") {
		t.Fatalf("single ring option should fail, got %+v", got)
	}

	unwearable := bulkTestRequest(nil, []*proto.BulkPoolItem{poolItem(bulkRingLimit1, f1, f2), poolItem(bulkRingLimit2, f1, f2)}, false)
	if got := BulkSimCount(unwearable); !strings.Contains(got.ErrorResult, "No wearable pair") {
		t.Fatalf("shared limit category should fail, got %+v", got)
	}
}

func TestBulkWithItemKeepsEnchantAndMigratesGems(t *testing.T) {
	bulkRegisterTestDatabase()
	existing := NewItem(ItemSpec{ID: bulkChest0, Enchant: bulkEnchantChest, Gems: []int32{bulkGemRed, bulkGemBlue}})
	newChest := NewItem(ItemSpec{ID: bulkChest1})
	swapped := bulkWithItem(existing, newChest)
	if swapped.Enchant.EffectID != bulkEnchantChest {
		t.Fatalf("chest enchant should carry over, got %d", swapped.Enchant.EffectID)
	}
	// Sockets [Blue, Red, Yellow]: red gem -> socket 1, blue gem -> socket 0.
	if got := []int32{swapped.Gems[0].ID, swapped.Gems[1].ID, swapped.Gems[2].ID}; got[0] != bulkGemBlue || got[1] != bulkGemRed || got[2] != 0 {
		t.Fatalf("gem migration: got %v", got)
	}

	twoHander := NewItem(ItemSpec{ID: bulkTwoHander, Enchant: bulkEnchantTwoHand})
	oneHander := NewItem(ItemSpec{ID: bulkSwordX})
	if got := bulkWithItem(twoHander, oneHander); got.Enchant.EffectID != 0 {
		t.Fatalf("two-hand-only enchant must not carry to a one-hander, got %d", got.Enchant.EffectID)
	}
	sword := NewItem(ItemSpec{ID: bulkSword0, Enchant: bulkEnchantWeapon})
	if got := bulkWithItem(sword, NewItem(ItemSpec{ID: bulkTwoHander})); got.Enchant.EffectID != bulkEnchantWeapon {
		t.Fatalf("generic weapon enchant should carry to a two-hander, got %d", got.Enchant.EffectID)
	}
}

func TestBulkWithEquippedItemSideEffects(t *testing.T) {
	bulkRegisterTestDatabase()
	var gear Equipment
	gear[proto.ItemSlot_ItemSlotMainHand] = NewItem(ItemSpec{ID: bulkSword0})
	gear[proto.ItemSlot_ItemSlotOffHand] = NewItem(ItemSpec{ID: bulkShield0})
	gear = bulkWithEquippedItem(gear, proto.ItemSlot_ItemSlotMainHand, NewItem(ItemSpec{ID: bulkTwoHander}))
	if gear[proto.ItemSlot_ItemSlotOffHand].ID != 0 {
		t.Fatalf("equipping a two-hander must clear the off hand")
	}

	gear = Equipment{}
	gear[proto.ItemSlot_ItemSlotHead] = NewItem(ItemSpec{ID: bulkHead0, Gems: []int32{bulkGemMeta, bulkGemUnique}})
	chest := NewItem(ItemSpec{ID: bulkChest0, Gems: []int32{bulkGemUnique, 0}})
	gear = bulkWithEquippedItem(gear, proto.ItemSlot_ItemSlotChest, chest)
	if gear[proto.ItemSlot_ItemSlotHead].Gems[1].ID != 0 {
		t.Fatalf("a unique gem on the new item must be removed from other slots")
	}
	if gear[proto.ItemSlot_ItemSlotChest].Gems[0].ID != bulkGemUnique {
		t.Fatalf("the new item keeps its unique gem")
	}
}

func TestBulkCandidateFallbackGemsAndMetaState(t *testing.T) {
	request := bulkTestRequest(map[proto.ItemSlot]*proto.ItemSpec{
		proto.ItemSlot_ItemSlotHead:  bulkSpec(bulkHead0),
		proto.ItemSlot_ItemSlotChest: bulkSpec(bulkChest0),
	}, []*proto.BulkPoolItem{poolItem(bulkChest1, proto.ItemSlot_ItemSlotChest)}, false)
	request.Settings.DefaultRedGem = bulkGemRed
	request.Settings.DefaultMetaGem = bulkGemMeta
	plan, base := bulkPlanFor(t, request)
	conditions := map[int32]*proto.BulkMetaGemCondition{bulkGemMeta: {GemId: bulkGemMeta, MinRed: 2}}
	fallback := bulkFallbackGems(request.Settings)

	// Both chests have one red socket, so with the head's red socket every
	// candidate holds 2 red gems: enough for the meta (needs 2). Only red
	// sockets get the red fallback; blue and yellow stay empty.
	for idx := 0; idx < plan.count; idx++ {
		gear := plan.buildCandidate(&base, idx, fallback, conditions)
		head := gear[proto.ItemSlot_ItemSlotHead]
		if head.Gems[0].ID != bulkGemMeta || head.Gems[1].ID != bulkGemRed {
			t.Fatalf("candidate %d head gems: %+v", idx, head.Gems)
		}
		chest := gear[proto.ItemSlot_ItemSlotChest]
		for socketIdx, color := range chest.GemSockets {
			want := int32(0)
			if color == proto.GemColor_GemColorRed {
				want = bulkGemRed
			}
			if chest.Gems[socketIdx].ID != want {
				t.Fatalf("candidate %d chest socket %d: got gem %d, want %d", idx, socketIdx, chest.Gems[socketIdx].ID, want)
			}
		}
		if red, _, _ := bulkGemColorCounts(&gear); red != 2 || head.Gems[0].Disabled {
			t.Fatalf("candidate %d: meta gem should be active with %d red gems", idx, red)
		}
	}

	// Raise the requirement past what the gear can hold and the meta goes inactive.
	conditions[bulkGemMeta].MinRed = 3
	gear := plan.buildCandidate(&base, 0, fallback, conditions)
	if !gear[proto.ItemSlot_ItemSlotHead].Gems[0].Disabled {
		t.Fatal("meta should be inactive when it needs 3 red gems and the gear holds 2")
	}
	if !gear.ToEquipmentSpecProto().Items[proto.ItemSlot_ItemSlotHead].MetaGemDisabled {
		t.Fatal("the inactive meta must be flagged in the equipment spec sent to the sim")
	}
}

func TestBulkStatConstraints(t *testing.T) {
	stat := func(s proto.Stat, op proto.BulkStatConstraintOp, value float64) *proto.BulkStatConstraint {
		return &proto.BulkStatConstraint{UnitStat: &proto.BulkStatConstraint_Stat{Stat: s}, Op: op, Value: value}
	}
	pseudo := func(p proto.PseudoStat, op proto.BulkStatConstraintOp, value float64) *proto.BulkStatConstraint {
		return &proto.BulkStatConstraint{UnitStat: &proto.BulkStatConstraint_PseudoStat{PseudoStat: p}, Op: op, Value: value}
	}
	finalStats := func(fireRes float64, critReduction float64) *proto.UnitStats {
		stats := &proto.UnitStats{Stats: make([]float64, int(proto.Stat_StatPhysicalDamage)+1), PseudoStats: make([]float64, int(proto.PseudoStat_PseudoStatReducedCritTakenPercent)+1)}
		stats.Stats[proto.Stat_StatFireResistance] = fireRes
		stats.PseudoStats[proto.PseudoStat_PseudoStatReducedCritTakenPercent] = critReduction
		return stats
	}
	op := proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThan
	cases := []struct {
		op    proto.BulkStatConstraintOp
		value float64
		want  bool
	}{
		{proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThan, 176, true},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThan, 175, false},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThanOrEqual, 175, true},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThanOrEqual, 174, false},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpEqual, 175, true},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpEqual, 174, false},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpLessThanOrEqual, 175, true},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpLessThanOrEqual, 176, false},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpLessThan, 174, true},
		{proto.BulkStatConstraintOp_BulkStatConstraintOpLessThan, 175, false},
	}
	for _, c := range cases {
		if got := bulkStatConstraintPasses(stat(proto.Stat_StatFireResistance, c.op, 175), c.value); got != c.want {
			t.Fatalf("%v with threshold 175 and value %v: got %v, want %v", c.op, c.value, got, c.want)
		}
	}
	_ = op

	constraints := []*proto.BulkStatConstraint{
		stat(proto.Stat_StatFireResistance, proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThan, 175),
		pseudo(proto.PseudoStat_PseudoStatReducedCritTakenPercent, proto.BulkStatConstraintOp_BulkStatConstraintOpGreaterThanOrEqual, 5.6),
	}
	if !bulkFinalStatsPassConstraints(constraints, finalStats(200, 5.6)) {
		t.Fatal("200 fire res and 5.6 crit reduction should pass")
	}
	if bulkFinalStatsPassConstraints(constraints, finalStats(175, 5.6)) {
		t.Fatal("175 fire res should fail a > 175 constraint")
	}
	if bulkFinalStatsPassConstraints(constraints, finalStats(200, 5.2)) {
		t.Fatal("5.2 crit reduction should fail a >= 5.6 constraint")
	}
	if !bulkFinalStatsPassConstraints(nil, &proto.UnitStats{}) {
		t.Fatal("no constraints always pass")
	}
	if got := bulkConstraintStatValue(&proto.BulkStatConstraint{}, finalStats(1, 1)); got != 0 {
		t.Fatalf("unset target reads as 0, got %v", got)
	}
	if got := bulkConstraintStatValue(constraints[0], nil); got != 0 {
		t.Fatalf("missing stats read as 0, got %v", got)
	}
}

func TestBulkSplitAndComboRange(t *testing.T) {
	mh, oh, f1, f2 := proto.ItemSlot_ItemSlotMainHand, proto.ItemSlot_ItemSlotOffHand, proto.ItemSlot_ItemSlotFinger1, proto.ItemSlot_ItemSlotFinger2
	request := bulkTestRequest(map[proto.ItemSlot]*proto.ItemSpec{
		proto.ItemSlot_ItemSlotHead: bulkSpec(bulkHead0), f1: bulkSpec(bulkRing0), f2: bulkSpec(bulkRing1),
		mh: bulkSpec(bulkSword0), oh: bulkSpec(bulkShield0), proto.ItemSlot_ItemSlotChest: bulkSpec(bulkChest0),
	}, []*proto.BulkPoolItem{
		poolItem(bulkHead1, proto.ItemSlot_ItemSlotHead), poolItem(bulkHead2, proto.ItemSlot_ItemSlotHead),
		poolItem(bulkRingA, f1, f2), poolItem(bulkRingB, f1, f2),
		poolItem(bulkChest1, proto.ItemSlot_ItemSlotChest),
		poolItem(bulkSwordX, mh), poolItem(bulkTwoHander, mh), poolItem(bulkShield, oh),
	}, false)
	plan, _ := bulkPlanFor(t, request)
	count := int32(plan.count) // 180

	// Even, contiguous pieces covering every combination exactly once.
	split := SplitBulkSimRequest(request, 7)
	if split.ErrorResult != "" || split.SplitsDone != 7 || len(split.Requests) != 7 {
		t.Fatalf("split into 7: %+v", split)
	}
	var next int32
	for i, piece := range split.Requests {
		if piece.ComboStart != next || piece.ComboEnd <= piece.ComboStart {
			t.Fatalf("piece %d range [%d, %d) does not continue from %d", i, piece.ComboStart, piece.ComboEnd, next)
		}
		if size := piece.ComboEnd - piece.ComboStart; size < count/7 || size > count/7+1 {
			t.Fatalf("piece %d has %d combinations, want %d or %d", i, size, count/7, count/7+1)
		}
		if len(piece.Pool) != len(request.Pool) || piece.Base == nil {
			t.Fatalf("piece %d lost its request contents", i)
		}
		next = piece.ComboEnd
	}
	if next != count {
		t.Fatalf("pieces end at %d, want %d", next, count)
	}
	// Ranges must be as the pieces will interpret them.
	for _, piece := range split.Requests {
		start, end, err := bulkComboRange(piece, plan.count)
		if err != nil || int32(start) != piece.ComboStart || int32(end) != piece.ComboEnd {
			t.Fatalf("bulkComboRange(%d, %d): %d %d %v", piece.ComboStart, piece.ComboEnd, start, end, err)
		}
	}

	// Never more pieces than combinations, never fewer than one.
	if got := SplitBulkSimRequest(request, 100000); got.SplitsDone != count || len(got.Requests) != int(count) {
		t.Fatalf("over-split: %d pieces, want %d", got.SplitsDone, count)
	}
	if got := SplitBulkSimRequest(request, 0); got.SplitsDone != 1 || got.Requests[0].ComboStart != 0 || got.Requests[0].ComboEnd != count {
		t.Fatalf("split into 0: %+v", got.Requests[0])
	}

	// A piece can't be split again, and an invalid batch fails at the split.
	if got := SplitBulkSimRequest(split.Requests[1], 2); !strings.Contains(got.ErrorResult, "already has a combination range") {
		t.Fatalf("re-split should fail, got %+v", got)
	}
	bad := bulkTestRequest(nil, []*proto.BulkPoolItem{poolItem(bulkRingA, f1, f2)}, false)
	if got := SplitBulkSimRequest(bad, 2); !strings.Contains(got.ErrorResult, "At least 2 items") {
		t.Fatalf("invalid batch should fail at the split, got %+v", got)
	}

	// Unset means the whole batch; anything outside it is rejected.
	if start, end, err := bulkComboRange(request, plan.count); err != nil || start != 0 || end != plan.count {
		t.Fatalf("unset range: %d %d %v", start, end, err)
	}
	for _, bad := range [][2]int32{{0, count + 1}, {5, 4}, {-1, 3}} {
		ranged := &proto.BulkSimRequest{ComboStart: bad[0], ComboEnd: bad[1]}
		if _, _, err := bulkComboRange(ranged, plan.count); err == nil {
			t.Fatalf("range %v should be rejected", bad)
		}
	}
}

func TestBulkCombineResults(t *testing.T) {
	combo := func(id int32, dps float64) *proto.BulkSimCombo {
		return &proto.BulkSimCombo{Equipment: &proto.EquipmentSpec{Items: []*proto.ItemSpec{{Id: id}}}, Dps: &proto.DistributionMetrics{Avg: dps}}
	}
	timings := func(build, gems, constraints, sims, total float64) *proto.BulkSimTimings {
		return &proto.BulkSimTimings{BuildMs: build, GemsMs: gems, ConstraintsMs: constraints, SimsMs: sims, TotalMs: total}
	}
	base := combo(1, 100)
	first := &proto.BulkSimResult{Results: []*proto.BulkSimCombo{combo(10, 130), combo(11, 110)}, Base: base, TotalCombinations: 9, SkippedByConstraints: 2, Timings: timings(1, 20, 3, 40, 70)}
	second := &proto.BulkSimResult{Results: []*proto.BulkSimCombo{combo(20, 120), combo(21, 140), combo(22, 105)}, TotalCombinations: 9, SkippedByConstraints: 1, Timings: timings(2, 10, 4, 30, 50)}

	// Pieces arrive in any order; the merge is the top N of all of them.
	merged := CombineBulkSimResults([]*proto.BulkSimResult{second, first}, 3)
	if merged.Error != nil {
		t.Fatalf("combine failed: %s", merged.Error.Message)
	}
	if got := []int32{merged.Results[0].Equipment.Items[0].Id, merged.Results[1].Equipment.Items[0].Id, merged.Results[2].Equipment.Items[0].Id}; len(merged.Results) != 3 || got[0] != 21 || got[1] != 10 || got[2] != 20 {
		t.Fatalf("merged ranking: %v", got)
	}
	if merged.Base != base || merged.TotalCombinations != 9 || merged.SkippedByConstraints != 3 {
		t.Fatalf("merged totals: base %v total %d skipped %d", merged.Base, merged.TotalCombinations, merged.SkippedByConstraints)
	}
	// Pieces run side by side, so each phase takes as long as its slowest piece.
	if want := timings(2, 20, 4, 40, 70); !googleProto.Equal(merged.Timings, want) {
		t.Fatalf("merged timings %+v, want %+v", merged.Timings, want)
	}

	// Default top N, an erroring piece, and a merge with no base.
	if got := CombineBulkSimResults([]*proto.BulkSimResult{first, second}, 0); len(got.Results) != 5 {
		t.Fatalf("default top n: got %d results", len(got.Results))
	}
	failed := &proto.BulkSimResult{Error: &proto.ErrorOutcome{Type: proto.ErrorOutcomeType_ErrorOutcomeAborted}}
	if got := CombineBulkSimResults([]*proto.BulkSimResult{first, failed}, 3); got.Error == nil || got.Error.Type != proto.ErrorOutcomeType_ErrorOutcomeAborted {
		t.Fatalf("a failed piece should fail the merge, got %+v", got)
	}
	if got := CombineBulkSimResults([]*proto.BulkSimResult{second}, 3); got.Error == nil {
		t.Fatal("a merge without the base gear should fail")
	}
	if got := CombineBulkSimResults(nil, 3); got.Error == nil {
		t.Fatal("an empty merge should fail")
	}
}
