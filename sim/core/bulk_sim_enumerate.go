package core

import (
	"fmt"
	"slices"

	"github.com/wowsims/tbc/sim/core/proto"
)

// Enumeration of gear combinations for the batch sim. Mirrors the browser's
// batch tab (ui/core/components/individual_sim_ui/bulk_tab.tsx and bulk/utils.ts):
// rings, trinkets and, for dual-wield specs, one-hand weapons are grouped and
// paired; every other slot contributes its options directly.

type bulkSlot int

const (
	bulkSlotHead bulkSlot = iota
	bulkSlotNeck
	bulkSlotShoulder
	bulkSlotBack
	bulkSlotChest
	bulkSlotWrist
	bulkSlotHands
	bulkSlotWaist
	bulkSlotLegs
	bulkSlotFeet
	bulkSlotFinger
	bulkSlotTrinket
	bulkSlotMainHand
	bulkSlotOffHand
	bulkSlotRanged
	bulkSlotHandWeapon // one-hand weapon pool for dual-wield specs
	numBulkSlots
)

var bulkSlotNames = map[bulkSlot]string{
	bulkSlotHead: "Head", bulkSlotNeck: "Neck", bulkSlotShoulder: "Shoulders", bulkSlotBack: "Back", bulkSlotChest: "Chest",
	bulkSlotWrist: "Wrist", bulkSlotHands: "Hands", bulkSlotWaist: "Waist", bulkSlotLegs: "Legs", bulkSlotFeet: "Feet",
	bulkSlotFinger: "Rings", bulkSlotTrinket: "Trinkets", bulkSlotMainHand: "Main Hand", bulkSlotOffHand: "Off Hand",
	bulkSlotRanged: "Ranged", bulkSlotHandWeapon: "Weapons",
}

var bulkSlotOfItemSlot = map[proto.ItemSlot]bulkSlot{
	proto.ItemSlot_ItemSlotHead: bulkSlotHead, proto.ItemSlot_ItemSlotNeck: bulkSlotNeck, proto.ItemSlot_ItemSlotShoulder: bulkSlotShoulder,
	proto.ItemSlot_ItemSlotBack: bulkSlotBack, proto.ItemSlot_ItemSlotChest: bulkSlotChest, proto.ItemSlot_ItemSlotWrist: bulkSlotWrist,
	proto.ItemSlot_ItemSlotHands: bulkSlotHands, proto.ItemSlot_ItemSlotWaist: bulkSlotWaist, proto.ItemSlot_ItemSlotLegs: bulkSlotLegs,
	proto.ItemSlot_ItemSlotFeet: bulkSlotFeet, proto.ItemSlot_ItemSlotFinger1: bulkSlotFinger, proto.ItemSlot_ItemSlotFinger2: bulkSlotFinger,
	proto.ItemSlot_ItemSlotTrinket1: bulkSlotTrinket, proto.ItemSlot_ItemSlotTrinket2: bulkSlotTrinket,
	proto.ItemSlot_ItemSlotMainHand: bulkSlotMainHand, proto.ItemSlot_ItemSlotOffHand: bulkSlotOffHand, proto.ItemSlot_ItemSlotRanged: bulkSlotRanged,
}

var bulkSlotSingleItemSlot = map[bulkSlot]proto.ItemSlot{
	bulkSlotHead: proto.ItemSlot_ItemSlotHead, bulkSlotNeck: proto.ItemSlot_ItemSlotNeck, bulkSlotShoulder: proto.ItemSlot_ItemSlotShoulder,
	bulkSlotBack: proto.ItemSlot_ItemSlotBack, bulkSlotChest: proto.ItemSlot_ItemSlotChest, bulkSlotWrist: proto.ItemSlot_ItemSlotWrist,
	bulkSlotHands: proto.ItemSlot_ItemSlotHands, bulkSlotWaist: proto.ItemSlot_ItemSlotWaist, bulkSlotLegs: proto.ItemSlot_ItemSlotLegs,
	bulkSlotFeet: proto.ItemSlot_ItemSlotFeet, bulkSlotRanged: proto.ItemSlot_ItemSlotRanged,
}

var bulkSlotPairItemSlots = map[bulkSlot][2]proto.ItemSlot{
	bulkSlotFinger:  {proto.ItemSlot_ItemSlotFinger1, proto.ItemSlot_ItemSlotFinger2},
	bulkSlotTrinket: {proto.ItemSlot_ItemSlotTrinket1, proto.ItemSlot_ItemSlotTrinket2},
}

func bulkSlotFor(slot proto.ItemSlot, canDualWield bool) bulkSlot {
	if canDualWield && (slot == proto.ItemSlot_ItemSlotMainHand || slot == proto.ItemSlot_ItemSlotOffHand) {
		return bulkSlotHandWeapon
	}
	return bulkSlotOfItemSlot[slot]
}

// One entry in a slot group: an item as specified (with its enchant, gems and
// suffix) plus the resolved item.
type bulkOption struct {
	item Item
	// True for the character's currently equipped item rather than a pool entry.
	equipped bool
}

// Same item ignoring enchant and gems, per EquippedItem.equals(other, true, true).
func (o *bulkOption) sameItem(p *bulkOption) bool {
	return o.item.ID == p.item.ID && o.item.RandomSuffix.ID == p.item.RandomSuffix.ID
}

// Whether a slot group may hold two copies of an item (both rings, both
// trinkets, or both hands), per BulkItemPickerGroup.canStackTwoCopies.
func bulkCanStackTwoCopies(slot bulkSlot, item *Item) bool {
	if item.Unique || item.LimitCategory != 0 {
		return false
	}
	switch slot {
	case bulkSlotFinger, bulkSlotTrinket:
		return true
	case bulkSlotHandWeapon:
		return item.HandType == proto.HandType_HandTypeOneHand
	}
	return false
}

type bulkAssignment struct {
	slot   proto.ItemSlot
	option *bulkOption
}

// The enumerable structure of a batch: per-group options, precomputed weapon
// combinations and ring/trinket pairs, and the total count.
type bulkPlan struct {
	groups       [numBulkSlots][]*bulkOption
	weaponCombos [][2]*bulkOption
	pairs        map[bulkSlot][][2]*bulkOption
	// Slots that contribute a dimension, in decode order after weapons.
	dimensions []bulkSlot
	count      int
}

func newBulkPlan(request *proto.BulkSimRequest, base *Equipment) (*bulkPlan, error) {
	plan := &bulkPlan{pairs: map[bulkSlot][][2]*bulkOption{}}
	canDualWield := request.CanDualWield
	settings := request.Settings
	if settings == nil {
		settings = &proto.BulkSettings{}
	}

	// Pool items first, then the equipped items, in the order the batch tab
	// registers them, so the same duplicate rules apply.
	for _, poolItem := range request.Pool {
		if poolItem.Item == nil || poolItem.Item.Id == 0 {
			continue
		}
		spec := bulkProtoToItemSpec(poolItem.Item)
		if _, ok := LookupItem(spec.ID); !ok {
			return nil, fmt.Errorf("batch pool item %d is not in the request database", spec.ID)
		}
		option := &bulkOption{item: NewItem(spec)}
		bulkNormalizeGems(&option.item)
		var added []bulkSlot
		for _, itemSlot := range poolItem.Slots {
			slot := bulkSlotFor(itemSlot, canDualWield)
			if slices.Contains(added, slot) {
				continue
			}
			added = append(added, slot)
			maxCopies := 1
			if bulkCanStackTwoCopies(slot, &option.item) {
				maxCopies = 2
			}
			copies := 0
			for _, existing := range plan.groups[slot] {
				if existing.item.ID == option.item.ID {
					copies++
				}
			}
			if copies >= maxCopies {
				continue
			}
			plan.groups[slot] = append(plan.groups[slot], option)
		}
	}
	for itemSlot := range base {
		item := base[itemSlot]
		if item.ID == 0 {
			continue
		}
		slot := bulkSlotFor(proto.ItemSlot(itemSlot), canDualWield)
		option := &bulkOption{item: bulkCloneItem(item), equipped: true}
		bulkNormalizeGems(&option.item)
		if !bulkCanStackTwoCopies(slot, &option.item) {
			plan.groups[slot] = slices.DeleteFunc(plan.groups[slot], func(existing *bulkOption) bool {
				return !existing.equipped && existing.item.ID == option.item.ID
			})
		}
		plan.groups[slot] = append(plan.groups[slot], option)
	}

	plan.weaponCombos = plan.buildWeaponCombos(settings, base)
	plan.count = max(len(plan.weaponCombos), 1)

	for slot := bulkSlotHead; slot < numBulkSlots; slot++ {
		if slot == bulkSlotMainHand || slot == bulkSlotOffHand || slot == bulkSlotHandWeapon {
			continue
		}
		options := plan.groups[slot]
		if len(options) == 0 {
			continue
		}
		if _, paired := bulkSlotPairItemSlots[slot]; paired {
			if len(options) < 2 {
				return nil, fmt.Errorf("At least 2 items must be selected for %s", bulkSlotNames[slot])
			}
			pairs := plan.buildGroupedPairs(slot, settings, base)
			if len(pairs) == 0 {
				return nil, fmt.Errorf("No wearable pair of items is available for %s", bulkSlotNames[slot])
			}
			plan.pairs[slot] = pairs
			plan.count *= len(pairs)
		} else {
			plan.count *= len(options)
		}
		plan.dimensions = append(plan.dimensions, slot)
	}
	return plan, nil
}

func bulkProtoToItemSpec(item *proto.ItemSpec) ItemSpec {
	return ItemSpec{
		ID:              item.Id,
		RandomSuffix:    item.RandomSuffix,
		Enchant:         item.Enchant,
		Gems:            item.Gems,
		MetaGemDisabled: item.MetaGemDisabled,
	}
}

// Mirrors BulkTab.getAllWeaponCombos plus weaponComboMatchesSettings.
func (plan *bulkPlan) buildWeaponCombos(settings *proto.BulkSettings, base *Equipment) [][2]*bulkOption {
	var combos [][2]*bulkOption
	isTwoHand := func(o *bulkOption) bool {
		ranged := o.item.RangedWeaponType
		return (ranged != proto.RangedWeaponType_RangedWeaponTypeUnknown && ranged != proto.RangedWeaponType_RangedWeaponTypeWand) ||
			o.item.HandType == proto.HandType_HandTypeTwoHand
	}

	var twoHanders []*bulkOption
	for _, slot := range []bulkSlot{bulkSlotMainHand, bulkSlotHandWeapon} {
		for _, option := range plan.groups[slot] {
			if isTwoHand(option) {
				twoHanders = append(twoHanders, option)
			}
		}
	}
	for _, twoHander := range twoHanders {
		combos = append(combos, [2]*bulkOption{twoHander, nil})
	}

	mainHands, offHands := plan.groups[bulkSlotMainHand], plan.groups[bulkSlotOffHand]
	if len(mainHands) > 0 {
		for _, mh := range mainHands {
			if slices.Contains(twoHanders, mh) {
				continue
			}
			if len(offHands) > 0 {
				for _, oh := range offHands {
					combos = append(combos, [2]*bulkOption{mh, oh})
				}
			} else {
				combos = append(combos, [2]*bulkOption{mh, nil})
			}
		}
	} else if len(offHands) > 0 {
		for _, oh := range offHands {
			combos = append(combos, [2]*bulkOption{nil, oh})
		}
	}

	if oneHanders := plan.groups[bulkSlotHandWeapon]; len(oneHanders) > 0 {
		var all []*bulkOption
		for _, option := range oneHanders {
			if !slices.Contains(twoHanders, option) {
				all = append(all, option)
			}
		}
		hasTwoCopies := func(option *bulkOption) bool {
			n := 0
			for _, other := range all {
				if other.sameItem(option) {
					n++
				}
			}
			return n >= 2
		}
		var options []*bulkOption
		for _, option := range all {
			if !slices.ContainsFunc(options, func(other *bulkOption) bool { return other.sameItem(option) }) {
				options = append(options, option)
			}
		}
		canGoMainHand := func(o *bulkOption) bool { return o.item.HandType != proto.HandType_HandTypeOffHand }
		canGoOffHand := func(o *bulkOption) bool { return o.item.HandType != proto.HandType_HandTypeMainHand }
		canFillBothHands := func(o *bulkOption) bool {
			return o.item.HandType == proto.HandType_HandTypeOneHand && !o.item.Unique && o.item.LimitCategory == 0
		}
		for i := range options {
			if canFillBothHands(options[i]) && hasTwoCopies(options[i]) {
				combos = append(combos, [2]*bulkOption{options[i], options[i]})
			}
			for j := i + 1; j < len(options); j++ {
				if canGoMainHand(options[i]) && canGoOffHand(options[j]) {
					combos = append(combos, [2]*bulkOption{options[i], options[j]})
				}
				if canGoMainHand(options[j]) && canGoOffHand(options[i]) {
					combos = append(combos, [2]*bulkOption{options[j], options[i]})
				}
			}
		}
	}

	// Frozen weapon slot and weapon type filters.
	frozenSlot := proto.ItemSlot(settings.FreezeWeaponSlot)
	var frozen *Item
	if frozenSlot == proto.ItemSlot_ItemSlotMainHand || frozenSlot == proto.ItemSlot_ItemSlotOffHand {
		if base[frozenSlot].ID != 0 {
			frozen = &base[frozenSlot]
		}
	}
	matchesTypeFilter := func(option *bulkOption, filter []proto.WeaponType) bool {
		if len(filter) == 0 {
			return true
		}
		return option != nil && option.item.WeaponType > proto.WeaponType_WeaponTypeUnknown && slices.Contains(filter, option.item.WeaponType)
	}
	return slices.DeleteFunc(combos, func(combo [2]*bulkOption) bool {
		if frozen != nil {
			var inSlot *bulkOption
			if frozenSlot == proto.ItemSlot_ItemSlotMainHand {
				inSlot = combo[0]
			} else {
				inSlot = combo[1]
			}
			if inSlot == nil || !bulkSameSpec(&inSlot.item, frozen) {
				return true
			}
		}
		return !matchesTypeFilter(combo[0], settings.FreezeMainhandWeaponSlots) || !matchesTypeFilter(combo[1], settings.FreezeOffhandWeaponSlots)
	})
}

// Mirrors BulkTab.getGroupedSlotPairs for rings and trinkets.
func (plan *bulkPlan) buildGroupedPairs(slot bulkSlot, settings *proto.BulkSettings, base *Equipment) [][2]*bulkOption {
	all := plan.groups[slot]
	hasTwoCopies := func(option *bulkOption) bool {
		n := 0
		for _, other := range all {
			if other.sameItem(option) {
				n++
			}
		}
		return n >= 2
	}
	var options []*bulkOption
	for _, option := range all {
		if !slices.ContainsFunc(options, func(other *bulkOption) bool { return other.sameItem(option) }) {
			options = append(options, option)
		}
	}
	canWearTogether := func(a, b *bulkOption) bool {
		if a.item.Unique && a.item.ID == b.item.ID {
			return false
		}
		if a.item.LimitCategory != 0 && a.item.LimitCategory == b.item.LimitCategory {
			return false
		}
		return true
	}

	frozenSlot := proto.ItemSlot(-1)
	switch slot {
	case bulkSlotFinger:
		frozenSlot = proto.ItemSlot(settings.FreezeRingSlot)
	case bulkSlotTrinket:
		frozenSlot = proto.ItemSlot(settings.FreezeTrinketSlot)
	}
	pairSlots := bulkSlotPairItemSlots[slot]
	if (frozenSlot == pairSlots[0] || frozenSlot == pairSlots[1]) && base[frozenSlot].ID != 0 {
		frozenItem := &base[frozenSlot]
		var frozen *bulkOption
		for _, option := range all {
			if option.equipped && bulkSameSpec(&option.item, frozenItem) {
				frozen = option
				break
			}
		}
		if frozen != nil {
			var pairs [][2]*bulkOption
			for _, option := range options {
				if (!frozen.sameItem(option) || hasTwoCopies(option)) && canWearTogether(frozen, option) {
					pairs = append(pairs, [2]*bulkOption{frozen, option})
				}
			}
			return pairs
		}
	}

	var pairs [][2]*bulkOption
	for i := range options {
		if hasTwoCopies(options[i]) && canWearTogether(options[i], options[i]) {
			pairs = append(pairs, [2]*bulkOption{options[i], options[i]})
		}
		for j := i + 1; j < len(options); j++ {
			if canWearTogether(options[i], options[j]) {
				pairs = append(pairs, [2]*bulkOption{options[i], options[j]})
			}
		}
	}
	return pairs
}

// Decodes a combination index into slot assignments, in the order the batch
// tab applies them: weapons first, then the remaining slots.
func (plan *bulkPlan) combo(idx int) []bulkAssignment {
	var assignments []bulkAssignment
	if n := len(plan.weaponCombos); n > 0 {
		combo := plan.weaponCombos[idx%n]
		idx /= n
		if combo[0] != nil {
			assignments = append(assignments, bulkAssignment{proto.ItemSlot_ItemSlotMainHand, combo[0]})
		}
		if combo[1] != nil {
			assignments = append(assignments, bulkAssignment{proto.ItemSlot_ItemSlotOffHand, combo[1]})
		}
	}
	for _, slot := range plan.dimensions {
		if pairs, paired := plan.pairs[slot]; paired {
			pair := pairs[idx%len(pairs)]
			idx /= len(pairs)
			slots := bulkSlotPairItemSlots[slot]
			assignments = append(assignments, bulkAssignment{slots[0], pair[0]}, bulkAssignment{slots[1], pair[1]})
		} else {
			options := plan.groups[slot]
			assignments = append(assignments, bulkAssignment{bulkSlotSingleItemSlot[slot], options[idx%len(options)]})
			idx /= len(options)
		}
	}
	return assignments
}

// Fallback gems by socket color from the batch settings.
func bulkFallbackGems(settings *proto.BulkSettings) map[proto.GemColor]Gem {
	gems := map[proto.GemColor]Gem{}
	for color, id := range map[proto.GemColor]int32{
		proto.GemColor_GemColorRed:       settings.DefaultRedGem,
		proto.GemColor_GemColorYellow:    settings.DefaultYellowGem,
		proto.GemColor_GemColorBlue:      settings.DefaultBlueGem,
		proto.GemColor_GemColorMeta:      settings.DefaultMetaGem,
		proto.GemColor_GemColorPrismatic: settings.DefaultPrismaticGem,
	} {
		if id == 0 {
			continue
		}
		if gem, ok := LookupGem(id); ok {
			gems[color] = gem
		}
	}
	return gems
}

// Builds the candidate gear set for a combination: the base gear with each
// assigned item swapped in (keeping the slot's enchant, migrating its gems)
// and the fallback gems socketed. Mirrors the candidate loop in runBatchSim.
func (plan *bulkPlan) buildCandidate(base *Equipment, idx int, fallbackGems map[proto.GemColor]Gem, metaConditions map[int32]*proto.BulkMetaGemCondition) Equipment {
	gear := *base
	for _, assignment := range plan.combo(idx) {
		var newItem Item
		if existing := gear[assignment.slot]; existing.ID != 0 {
			newItem = bulkWithItem(existing, assignment.option.item)
		} else {
			newItem = bulkCloneItem(assignment.option.item)
		}
		bulkNormalizeGems(&newItem)
		gear = bulkWithEquippedItem(gear, assignment.slot, newItem)
		for socketIdx, socketColor := range newItem.GemSockets {
			if gem, ok := fallbackGems[socketColor]; ok {
				gear = bulkWithGem(gear, assignment.slot, socketIdx, gem)
			}
		}
	}
	bulkApplyMetaGemState(&gear, metaConditions)
	return gear
}
