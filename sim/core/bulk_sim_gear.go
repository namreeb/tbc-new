package core

import (
	"slices"

	"github.com/wowsims/tbc/sim/core/proto"
)

// Gear manipulation for the batch sim. These mirror the browser's Gear /
// EquippedItem helpers (ui/core/proto_utils/gear.ts, equipped_item.ts,
// gems.ts, utils.ts) so a candidate built here matches one built in the UI.

// Socket color -> gem colors that fit it, per ui/core/proto_utils/gems.ts.
var bulkSocketMatchingColors = map[proto.GemColor][]proto.GemColor{
	proto.GemColor_GemColorMeta:   {proto.GemColor_GemColorMeta},
	proto.GemColor_GemColorBlue:   {proto.GemColor_GemColorBlue, proto.GemColor_GemColorPurple, proto.GemColor_GemColorGreen, proto.GemColor_GemColorPrismatic},
	proto.GemColor_GemColorRed:    {proto.GemColor_GemColorRed, proto.GemColor_GemColorPurple, proto.GemColor_GemColorOrange, proto.GemColor_GemColorPrismatic},
	proto.GemColor_GemColorYellow: {proto.GemColor_GemColorYellow, proto.GemColor_GemColorOrange, proto.GemColor_GemColorGreen, proto.GemColor_GemColorPrismatic},
	proto.GemColor_GemColorPrismatic: {
		proto.GemColor_GemColorRed, proto.GemColor_GemColorOrange, proto.GemColor_GemColorYellow,
		proto.GemColor_GemColorGreen, proto.GemColor_GemColorBlue, proto.GemColor_GemColorPurple, proto.GemColor_GemColorPrismatic,
	},
}

// Whether a gem of the given color matches (earns the bonus of) a socket color.
func bulkGemColorMatchesSocket(gemColor, socketColor proto.GemColor) bool {
	return gemColor == socketColor || slices.Contains(bulkSocketMatchingColors[socketColor], gemColor)
}

// Whether a gem of the given color can be socketed at all in a socket color.
func bulkGemEligibleForSocket(gemColor, socketColor proto.GemColor) bool {
	if socketColor == proto.GemColor_GemColorMeta {
		return gemColor == proto.GemColor_GemColorMeta
	}
	return gemColor != proto.GemColor_GemColorMeta
}

// Copies an item so its gem slice can be modified without touching the source.
func bulkCloneItem(item Item) Item {
	item.Gems = slices.Clone(item.Gems)
	return item
}

// Ensures the item's gem slice covers every socket (empty sockets are zero Gems).
func bulkNormalizeGems(item *Item) {
	if len(item.Gems) < len(item.GemSockets) {
		gems := make([]Gem, len(item.GemSockets))
		copy(gems, item.Gems)
		item.Gems = gems
	}
}

// Slots an enchant may be applied to, per ui/core/proto_utils/utils.ts getEligibleEnchantSlots.
func bulkEligibleEnchantSlots(enchant *Enchant) []proto.ItemSlot {
	var slots []proto.ItemSlot
	for _, itemType := range append([]proto.ItemType{enchant.Type}, enchant.ExtraTypes...) {
		if mapped, ok := itemTypeToSlotsMap[itemType]; ok {
			slots = append(slots, mapped...)
		} else if itemType == proto.ItemType_ItemTypeWeapon {
			slots = append(slots, proto.ItemSlot_ItemSlotMainHand, proto.ItemSlot_ItemSlotOffHand)
		}
	}
	return slots
}

// Whether an enchant can be applied to an item, per ui/core/proto_utils/utils.ts enchantAppliesToItem.
func bulkEnchantAppliesToItem(enchant *Enchant, item *Item) bool {
	if enchant.EffectID == 0 {
		return false
	}
	itemSlots := eligibleSlotsForItem(item)
	if !slices.ContainsFunc(bulkEligibleEnchantSlots(enchant), func(slot proto.ItemSlot) bool { return slices.Contains(itemSlots, slot) }) {
		return false
	}
	if enchant.EnchantType == proto.EnchantType_EnchantTypeTwoHand && item.HandType != proto.HandType_HandTypeTwoHand {
		return false
	}
	if enchant.EnchantType == proto.EnchantType_EnchantTypeStaff && item.WeaponType != proto.WeaponType_WeaponTypeStaff {
		return false
	}
	if enchant.EnchantType == proto.EnchantType_EnchantTypeShield && item.WeaponType != proto.WeaponType_WeaponTypeShield {
		return false
	}
	isOffHandEnchant := enchant.EnchantType == proto.EnchantType_EnchantTypeOffHand
	isOffHandItem := item.WeaponType == proto.WeaponType_WeaponTypeOffHand ||
		(item.WeaponType == proto.WeaponType_WeaponTypeShield && enchant.EnchantType != proto.EnchantType_EnchantTypeShield)
	if isOffHandEnchant != isOffHandItem {
		return false
	}
	if enchant.Type == proto.ItemType_ItemTypeRanged {
		switch item.RangedWeaponType {
		case proto.RangedWeaponType_RangedWeaponTypeBow, proto.RangedWeaponType_RangedWeaponTypeCrossbow, proto.RangedWeaponType_RangedWeaponTypeGun:
		default:
			return false
		}
	}
	if item.RangedWeaponType != proto.RangedWeaponType_RangedWeaponTypeWand && item.RangedWeaponType > 0 && enchant.Type != proto.ItemType_ItemTypeRanged {
		return false
	}
	return true
}

// Swaps a new item into the place of an equipped one, keeping the enchant when
// it applies and moving the gems into the new item's sockets, matching colors
// where possible. Mirrors EquippedItem.withItem.
func bulkWithItem(existing Item, newItem Item) Item {
	result := bulkCloneItem(newItem)
	result.Enchant = Enchant{}
	if bulkEnchantAppliesToItem(&existing.Enchant, &newItem) {
		result.Enchant = existing.Enchant
	}

	result.Gems = make([]Gem, len(newItem.GemSockets))
	existingGems := existing.Gems
	if len(existingGems) > len(existing.GemSockets) {
		existingGems = existingGems[:len(existing.GemSockets)]
	}
	for _, gem := range existingGems {
		if gem.ID == 0 {
			continue
		}
		matching, eligible := -1, -1
		for socketIdx, socketColor := range newItem.GemSockets {
			if result.Gems[socketIdx].ID != 0 {
				continue
			}
			if matching < 0 && bulkGemColorMatchesSocket(gem.Color, socketColor) {
				matching = socketIdx
			}
			if eligible < 0 && bulkGemEligibleForSocket(gem.Color, socketColor) {
				eligible = socketIdx
			}
		}
		if matching >= 0 {
			result.Gems[matching] = gem
		} else if eligible >= 0 {
			result.Gems[eligible] = gem
		}
	}
	return result
}

// Removes every socketed copy of a gem id across the gear set.
func bulkRemoveGemsWithId(gear *Equipment, gemID int32) {
	for slot := range gear {
		for socketIdx, gem := range gear[slot].Gems {
			if gem.ID == gemID {
				gear[slot] = bulkCloneItem(gear[slot])
				gear[slot].Gems[socketIdx] = Gem{}
			}
		}
	}
}

// Whether a main hand / off hand pairing is wearable, per validWeaponCombo in
// ui/core/proto_utils/utils.ts: a two-hander in either hand invalidates the pair.
func bulkValidWeaponCombo(mainHand, offHand *Item) bool {
	if mainHand.ID != 0 && mainHand.HandType == proto.HandType_HandTypeTwoHand {
		return false
	}
	if offHand.ID != 0 && offHand.HandType == proto.HandType_HandTypeTwoHand {
		return false
	}
	return true
}

// Equips an item into a slot, applying the same side effects as Gear.withEquippedItem:
// unique gems and unique / limit-category items are removed elsewhere, and an
// invalid weapon pairing is resolved by clearing the other hand.
func bulkWithEquippedItem(gear Equipment, slot proto.ItemSlot, item Item) Equipment {
	for _, gem := range item.Gems {
		if gem.ID != 0 && gem.Unique {
			bulkRemoveGemsWithId(&gear, gem.ID)
		}
	}
	if item.Unique || item.LimitCategory != 0 {
		for other := range gear {
			if gear[other].ID == 0 {
				continue
			}
			if (item.LimitCategory != 0 && gear[other].LimitCategory == item.LimitCategory) || (item.Unique && gear[other].ID == item.ID) {
				gear[other] = Item{}
			}
		}
	}

	gear[slot] = item

	mh, oh := proto.ItemSlot_ItemSlotMainHand, proto.ItemSlot_ItemSlotOffHand
	if !bulkValidWeaponCombo(&gear[mh], &gear[oh]) {
		if slot == oh {
			if gear[oh].HandType == proto.HandType_HandTypeTwoHand {
				gear[oh] = Item{}
			}
			gear[mh] = Item{}
		} else {
			gear[oh] = Item{}
		}
	}
	return gear
}

// Sockets a gem, removing other copies first when it is unique. Mirrors Gear.withGem.
func bulkWithGem(gear Equipment, slot proto.ItemSlot, socketIdx int, gem Gem) Equipment {
	if gear[slot].ID == 0 || socketIdx >= len(gear[slot].GemSockets) {
		return gear
	}
	if gem.Unique {
		bulkRemoveGemsWithId(&gear, gem.ID)
	}
	item := bulkCloneItem(gear[slot])
	bulkNormalizeGems(&item)
	item.Gems[socketIdx] = gem
	gear[slot] = item
	return gear
}

// Counts of gems matching each primary color across the gear set, per Gear.gemColorCounts.
func bulkGemColorCounts(gear *Equipment) (red, yellow, blue int) {
	for slot := range gear {
		for _, gem := range gear[slot].Gems {
			if gem.ID == 0 {
				continue
			}
			if bulkGemColorMatchesSocket(gem.Color, proto.GemColor_GemColorRed) {
				red++
			}
			if bulkGemColorMatchesSocket(gem.Color, proto.GemColor_GemColorYellow) {
				yellow++
			}
			if bulkGemColorMatchesSocket(gem.Color, proto.GemColor_GemColorBlue) {
				blue++
			}
		}
	}
	return
}

func bulkMetaGemConditionMet(cond *proto.BulkMetaGemCondition, red, yellow, blue int) bool {
	if int32(red) < cond.MinRed || int32(yellow) < cond.MinYellow || int32(blue) < cond.MinBlue {
		return false
	}
	if cond.CompareColorGreater == proto.GemColor_GemColorUnknown {
		return true
	}
	count := func(color proto.GemColor) int {
		switch color {
		case proto.GemColor_GemColorRed:
			return red
		case proto.GemColor_GemColorYellow:
			return yellow
		case proto.GemColor_GemColorBlue:
			return blue
		}
		return 0
	}
	return count(cond.CompareColorGreater) > count(cond.CompareColorLesser)
}

// Marks the meta gem disabled when its activation condition is not met, as the
// UI does before sending gear to the sim. An unknown meta gem is left active.
func bulkApplyMetaGemState(gear *Equipment, conditions map[int32]*proto.BulkMetaGemCondition) {
	red, yellow, blue := bulkGemColorCounts(gear)
	for slot := range gear {
		for socketIdx, gem := range gear[slot].Gems {
			if gem.ID == 0 || gem.Color != proto.GemColor_GemColorMeta {
				continue
			}
			disabled := false
			if cond, ok := conditions[gem.ID]; ok {
				disabled = !bulkMetaGemConditionMet(cond, red, yellow, blue)
			}
			if gem.Disabled != disabled {
				gear[slot] = bulkCloneItem(gear[slot])
				gear[slot].Gems[socketIdx].Disabled = disabled
			}
		}
	}
}

// Full item-spec equality: same item, suffix, enchant and gems.
func bulkSameSpec(a, b *Item) bool {
	if a.ID != b.ID || a.RandomSuffix.ID != b.RandomSuffix.ID || a.Enchant.EffectID != b.Enchant.EffectID {
		return false
	}
	gemID := func(gems []Gem, idx int) int32 {
		if idx < len(gems) {
			return gems[idx].ID
		}
		return 0
	}
	for idx := 0; idx < max(len(a.Gems), len(b.Gems)); idx++ {
		if gemID(a.Gems, idx) != gemID(b.Gems, idx) {
			return false
		}
	}
	return true
}

func bulkSameGear(a, b *Equipment) bool {
	for slot := range a {
		if !bulkSameSpec(&a[slot], &b[slot]) {
			return false
		}
		if len(a[slot].Gems) > 0 || len(b[slot].Gems) > 0 {
			for idx := 0; idx < min(len(a[slot].Gems), len(b[slot].Gems)); idx++ {
				if a[slot].Gems[idx].Disabled != b[slot].Gems[idx].Disabled {
					return false
				}
			}
		}
	}
	return true
}
