package core

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/wowsims/tbc/sim/core/proto"
)

// Gem choice for batch candidates. A port of the gem half of the browser's
// ReforgeOptimizer (ui/core/components/suggest_reforges_action.tsx): the same
// model of the problem (per-socket gem variables scored by stat weights,
// socket bonuses, meta gem color rules, unique gems, hard and soft caps
// enforced iteratively), solved exactly by branch and bound instead of a
// linear-programming library. Where the two solvers meet several gemmings of
// equal score they may pick different ones; everywhere else they agree.

// Rating-to-percent conversions, per ui/core/constants/mechanics.ts.
const (
	bulkExpertisePerQuarterPercent = 3.942308
	bulkPhysicalHasteRatingPerPct  = 15.769233
	bulkSpellHasteRatingPerPct     = 15.769233
	bulkSpellCritRatingPerPct      = 22.076923
	bulkPhysicalCritRatingPerPct   = 22.076923
	bulkSpellHitRatingPerPct       = 12.615385
	bulkPhysicalHitRatingPerPct    = 15.769233
	bulkDefenseRatingPerDefense    = 2.365385
	bulkCritChancePerDefense       = 0.04
	bulkResilienceRatingPerCritPct = 39.4231
	bulkGemSolverNodeBudget        = 4_000_000
	bulkCapInfeasibleMessage       = "The specified stat caps are impossible to achieve. Consider changing any upper bound stat caps to lower bounds instead."
	bulkGemZeroGap                 = 1e-12
	bulkScoreEpsilon               = 1e-9
)

var (
	bulkNumStats       = len(proto.Stat_name)
	bulkNumPseudoStats = len(proto.PseudoStat_name)
)

// A stat vector over both Stat and PseudoStat, the unit the optimizer works in.
type bulkUnitStats struct {
	stats  []float64
	pseudo []float64
}

func newBulkUnitStats() bulkUnitStats {
	return bulkUnitStats{stats: make([]float64, bulkNumStats), pseudo: make([]float64, bulkNumPseudoStats)}
}

func bulkUnitStatsFromProto(p *proto.UnitStats) bulkUnitStats {
	u := newBulkUnitStats()
	if p == nil {
		return u
	}
	copy(u.stats, p.Stats)
	copy(u.pseudo, p.PseudoStats)
	return u
}

func (u bulkUnitStats) clone() bulkUnitStats {
	return bulkUnitStats{stats: slices.Clone(u.stats), pseudo: slices.Clone(u.pseudo)}
}

// Sparse stat contributions of one choice (a gem in a socket, or a socket bonus).
type bulkCoeffs struct {
	stats  map[proto.Stat]float64
	pseudo map[proto.PseudoStat]float64
}

func newBulkCoeffs() bulkCoeffs {
	return bulkCoeffs{stats: map[proto.Stat]float64{}, pseudo: map[proto.PseudoStat]float64{}}
}

func (c bulkCoeffs) clone() bulkCoeffs {
	out := newBulkCoeffs()
	for k, v := range c.stats {
		out.stats[k] = v
	}
	for k, v := range c.pseudo {
		out.pseudo[k] = v
	}
	return out
}

func (c bulkCoeffs) addAll(other bulkCoeffs) {
	for k, v := range other.stats {
		c.stats[k] += v
	}
	for k, v := range other.pseudo {
		c.pseudo[k] += v
	}
}

func (c bulkCoeffs) score(weights bulkUnitStats) float64 {
	total := 0.0
	for stat, v := range c.stats {
		total += weights.stats[stat] * v
	}
	for pseudo, v := range c.pseudo {
		total += weights.pseudo[pseudo] * v
	}
	return total
}

func (c bulkCoeffs) empty() bool { return len(c.stats) == 0 && len(c.pseudo) == 0 }

// A soft cap with breakpoints made relative to the candidate's base stats.
type bulkSoftCap struct {
	stat       proto.Stat
	pseudoStat proto.PseudoStat
	isPseudo   bool
	capType    proto.BulkStatCapType
	breaks     []float64
	postCapEPs []float64
}

func (c *bulkSoftCap) matchesStat(stat proto.Stat) bool      { return !c.isPseudo && c.stat == stat }
func (c *bulkSoftCap) matchesPseudo(p proto.PseudoStat) bool { return c.isPseudo && c.pseudoStat == p }

// Children of a parent rating stat, per UnitStat.getChildren.
func bulkChildPseudoStats(parent proto.Stat) []proto.PseudoStat {
	switch parent {
	case proto.Stat_StatMeleeHitRating:
		return []proto.PseudoStat{proto.PseudoStat_PseudoStatMeleeHitPercent, proto.PseudoStat_PseudoStatRangedHitPercent}
	case proto.Stat_StatMeleeCritRating:
		return []proto.PseudoStat{proto.PseudoStat_PseudoStatMeleeCritPercent, proto.PseudoStat_PseudoStatRangedCritPercent}
	case proto.Stat_StatMeleeHasteRating:
		return []proto.PseudoStat{proto.PseudoStat_PseudoStatMeleeHastePercent, proto.PseudoStat_PseudoStatRangedHastePercent}
	case proto.Stat_StatSpellHitRating:
		return []proto.PseudoStat{
			proto.PseudoStat_PseudoStatSpellHitPercent, proto.PseudoStat_PseudoStatSchoolHitPercentArcane, proto.PseudoStat_PseudoStatSchoolHitPercentFire,
			proto.PseudoStat_PseudoStatSchoolHitPercentFrost, proto.PseudoStat_PseudoStatSchoolHitPercentHoly, proto.PseudoStat_PseudoStatSchoolHitPercentNature,
			proto.PseudoStat_PseudoStatSchoolHitPercentShadow,
		}
	case proto.Stat_StatSpellCritRating:
		return []proto.PseudoStat{proto.PseudoStat_PseudoStatSpellCritPercent}
	case proto.Stat_StatSpellHasteRating:
		return []proto.PseudoStat{proto.PseudoStat_PseudoStatSpellHastePercent}
	case proto.Stat_StatResilienceRating, proto.Stat_StatDefenseRating:
		return []proto.PseudoStat{proto.PseudoStat_PseudoStatReducedCritTakenPercent}
	}
	return nil
}

var bulkSchoolHitPseudoStats = []proto.PseudoStat{
	proto.PseudoStat_PseudoStatSchoolHitPercentArcane, proto.PseudoStat_PseudoStatSchoolHitPercentFire, proto.PseudoStat_PseudoStatSchoolHitPercentFrost,
	proto.PseudoStat_PseudoStatSchoolHitPercentHoly, proto.PseudoStat_PseudoStatSchoolHitPercentNature, proto.PseudoStat_PseudoStatSchoolHitPercentShadow,
}

// Rating -> percent for a parent stat's child pseudo stat, per convertStatToChildPseudoStat.
func bulkRatingToChildPercent(parent proto.Stat, child proto.PseudoStat, amount float64) (float64, bool) {
	if child == proto.PseudoStat_PseudoStatReducedCritTakenPercent {
		switch parent {
		case proto.Stat_StatDefenseRating:
			return amount / bulkDefenseRatingPerDefense * bulkCritChancePerDefense, true
		case proto.Stat_StatResilienceRating:
			return amount / bulkResilienceRatingPerCritPct, true
		}
		return 0, false
	}
	switch child {
	case proto.PseudoStat_PseudoStatMeleeHitPercent, proto.PseudoStat_PseudoStatRangedHitPercent:
		return amount / bulkPhysicalHitRatingPerPct, true
	case proto.PseudoStat_PseudoStatSpellHitPercent, proto.PseudoStat_PseudoStatSchoolHitPercentArcane, proto.PseudoStat_PseudoStatSchoolHitPercentFire,
		proto.PseudoStat_PseudoStatSchoolHitPercentFrost, proto.PseudoStat_PseudoStatSchoolHitPercentHoly, proto.PseudoStat_PseudoStatSchoolHitPercentNature,
		proto.PseudoStat_PseudoStatSchoolHitPercentShadow:
		return amount / bulkSpellHitRatingPerPct, true
	case proto.PseudoStat_PseudoStatMeleeCritPercent, proto.PseudoStat_PseudoStatRangedCritPercent:
		return amount / bulkPhysicalCritRatingPerPct, true
	case proto.PseudoStat_PseudoStatSpellCritPercent:
		return amount / bulkSpellCritRatingPerPct, true
	case proto.PseudoStat_PseudoStatMeleeHastePercent, proto.PseudoStat_PseudoStatRangedHastePercent:
		return amount / bulkPhysicalHasteRatingPerPct, true
	case proto.PseudoStat_PseudoStatSpellHastePercent:
		return amount / bulkSpellHasteRatingPerPct, true
	}
	return 0, false
}

// Percent -> rating for a child pseudo stat (1 percent's worth), per convertPercentToRating.
func bulkChildPercentToRating(parent proto.Stat, child proto.PseudoStat) (float64, bool) {
	if child == proto.PseudoStat_PseudoStatReducedCritTakenPercent {
		switch parent {
		case proto.Stat_StatDefenseRating:
			return bulkDefenseRatingPerDefense / bulkCritChancePerDefense, true
		case proto.Stat_StatResilienceRating:
			return bulkResilienceRatingPerCritPct, true
		}
		return 0, false
	}
	if perPct, ok := bulkRatingToChildPercent(parent, child, 1); ok && perPct > 0 {
		return 1 / perPct, true
	}
	return 0, false
}

// The gem colors a gem counts for in meta gem conditions, per gemColorsToMatchingSocket.
func bulkGemMetaColors(color proto.GemColor) []proto.GemColor {
	switch color {
	case proto.GemColor_GemColorMeta, proto.GemColor_GemColorRed, proto.GemColor_GemColorBlue, proto.GemColor_GemColorYellow:
		return []proto.GemColor{color}
	case proto.GemColor_GemColorGreen:
		return []proto.GemColor{proto.GemColor_GemColorBlue, proto.GemColor_GemColorYellow}
	case proto.GemColor_GemColorOrange:
		return []proto.GemColor{proto.GemColor_GemColorRed, proto.GemColor_GemColorYellow}
	case proto.GemColor_GemColorPurple:
		return []proto.GemColor{proto.GemColor_GemColorRed, proto.GemColor_GemColorBlue}
	}
	return nil
}

// Static (character-level) optimizer inputs shared by every candidate.
type bulkGemOptimizer struct {
	base           *proto.RaidSimRequest
	race           proto.Race
	preCapEPs      bulkUnitStats
	statCaps       bulkUnitStats
	undershootCaps bulkUnitStats
	softCaps       []*proto.BulkStatCap
	frozenSlots    map[proto.ItemSlot]bool
	metaConditions map[int32]*proto.BulkMetaGemCondition
	debuffStats    bulkUnitStats
	// Eligible gems by socket color, before per-candidate cap pruning.
	eligibleByColor map[proto.GemColor][]bulkEligibleGem
}

type bulkEligibleGem struct {
	gem  Gem
	isJC bool
}

func newBulkGemOptimizer(request *proto.BulkSimRequest, player *proto.Player) (*bulkGemOptimizer, error) {
	settings := request.Gems
	o := &bulkGemOptimizer{
		base:            request.Base,
		race:            player.Race,
		preCapEPs:       bulkUnitStatsFromProto(settings.StatWeights),
		statCaps:        bulkUnitStatsFromProto(settings.StatCaps),
		undershootCaps:  bulkUnitStatsFromProto(settings.UndershootCaps),
		softCaps:        settings.SoftCaps,
		frozenSlots:     map[proto.ItemSlot]bool{},
		metaConditions:  map[int32]*proto.BulkMetaGemCondition{},
		eligibleByColor: map[proto.GemColor][]bulkEligibleGem{},
	}
	for _, slot := range settings.FrozenSlots {
		o.frozenSlots[slot] = true
	}
	for _, cond := range settings.MetaGemConditions {
		o.metaConditions[cond.GemId] = cond
	}
	o.debuffStats = bulkDebuffStats(request.Base)

	var eligible []bulkEligibleGem
	for _, option := range settings.EligibleGems {
		gem, ok := LookupGem(option.GemId)
		if !ok {
			return nil, fmt.Errorf("eligible gem %d is not in the request database", option.GemId)
		}
		eligible = append(eligible, bulkEligibleGem{gem: gem, isJC: option.Jewelcrafting})
	}
	o.setEligibleGems(eligible)
	return o, nil
}

// Partitions the eligible (non-meta) gems by the socket colors they fit.
func (o *bulkGemOptimizer) setEligibleGems(eligible []bulkEligibleGem) {
	o.eligibleByColor = map[proto.GemColor][]bulkEligibleGem{}
	for _, color := range []proto.GemColor{proto.GemColor_GemColorPrismatic, proto.GemColor_GemColorRed, proto.GemColor_GemColorBlue, proto.GemColor_GemColorYellow} {
		for _, e := range eligible {
			if e.gem.Color != proto.GemColor_GemColorMeta && bulkGemColorMatchesSocket(e.gem.Color, color) {
				o.eligibleByColor[color] = append(o.eligibleByColor[color], e)
			}
		}
	}
}

// Stats the character sheet adds for target debuffs, per CharacterStats.getDebuffStats.
// (The hunter-with-Expose-Weakness special case uses the debuff's configured agility.)
func bulkDebuffStats(base *proto.RaidSimRequest) bulkUnitStats {
	u := newBulkUnitStats()
	if base == nil || base.Raid == nil || base.Raid.Debuffs == nil {
		return u
	}
	debuffs := base.Raid.Debuffs
	if debuffs.FaerieFire == proto.TristateEffect_TristateEffectImproved {
		u.pseudo[proto.PseudoStat_PseudoStatMeleeHitPercent] += 3
		u.pseudo[proto.PseudoStat_PseudoStatRangedHitPercent] += 3
	}
	if debuffs.ImprovedSealOfTheCrusader != proto.TristateEffect_TristateEffectMissing {
		u.pseudo[proto.PseudoStat_PseudoStatMeleeCritPercent] += 3
		u.pseudo[proto.PseudoStat_PseudoStatRangedCritPercent] += 3
		u.pseudo[proto.PseudoStat_PseudoStatSpellCritPercent] += 3
	}
	if debuffs.ExposeWeaknessUptime != 0 && debuffs.ExposeWeaknessHunterAgility != 0 {
		u.stats[proto.Stat_StatAttackPower] += debuffs.ExposeWeaknessHunterAgility * 0.25
		u.stats[proto.Stat_StatRangedAttackPower] += debuffs.ExposeWeaknessHunterAgility * 0.25
	}
	if debuffs.HuntersMark != proto.TristateEffect_TristateEffectMissing {
		u.stats[proto.Stat_StatRangedAttackPower] += 440
		if debuffs.HuntersMark == proto.TristateEffect_TristateEffectImproved {
			u.stats[proto.Stat_StatAttackPower] += 110
		}
	}
	return u
}

// applyReforgeStat: a stat amount becomes either the stat itself (when it has
// a weight) or its weighted child percentages.
func (o *bulkGemOptimizer) applyStat(coeffs bulkCoeffs, stat proto.Stat, amount float64, preCapEPs bulkUnitStats) {
	if stat == proto.Stat_StatSpirit && o.race == proto.Race_RaceHuman {
		amount *= 1.1
	}
	if stat == proto.Stat_StatIntellect && o.race == proto.Race_RaceGnome {
		amount *= 1.05
	}
	if preCapEPs.stats[stat] != 0 {
		coeffs.stats[stat] += amount
		return
	}
	for _, child := range bulkChildPseudoStats(stat) {
		if preCapEPs.pseudo[child] == 0 {
			continue
		}
		if converted, ok := bulkRatingToChildPercent(stat, child, amount); ok {
			coeffs.pseudo[child] += converted
		}
	}
}

func bulkStatHasCap(stat proto.Stat, caps bulkUnitStats, softCaps []bulkSoftCap) bool {
	if caps.stats[stat] != 0 {
		return true
	}
	return slices.ContainsFunc(softCaps, func(c bulkSoftCap) bool { return c.matchesStat(stat) })
}

func bulkPseudoHasCap(p proto.PseudoStat, caps bulkUnitStats, softCaps []bulkSoftCap) bool {
	if caps.pseudo[p] != 0 {
		return true
	}
	return slices.ContainsFunc(softCaps, func(c bulkSoftCap) bool { return c.matchesPseudo(p) })
}

// A stat is "capped" (already at or over its cap) when its remaining gap is negative.
func bulkStatIsCapped(stat proto.Stat, caps bulkUnitStats) bool      { return caps.stats[stat] < 0 }
func bulkPseudoIsCapped(p proto.PseudoStat, caps bulkUnitStats) bool { return caps.pseudo[p] < 0 }

func bulkCoeffsIncludeStatWithCap(c bulkCoeffs, caps bulkUnitStats, softCaps []bulkSoftCap) bool {
	for stat := range c.stats {
		if bulkStatHasCap(stat, caps, softCaps) {
			return true
		}
	}
	for p := range c.pseudo {
		if bulkPseudoHasCap(p, caps, softCaps) {
			return true
		}
	}
	return false
}

func bulkCoeffsIncludeCappedStat(c bulkCoeffs, caps bulkUnitStats) bool {
	for stat := range c.stats {
		if bulkStatIsCapped(stat, caps) {
			return true
		}
	}
	for p := range c.pseudo {
		if bulkPseudoIsCapped(p, caps) {
			return true
		}
	}
	return false
}

func bulkCoeffsCappedCount(c bulkCoeffs, caps bulkUnitStats, softCaps []bulkSoftCap) int {
	n := 0
	for stat := range c.stats {
		if bulkStatHasCap(stat, caps, softCaps) {
			n++
		}
	}
	for p := range c.pseudo {
		if bulkPseudoHasCap(p, caps, softCaps) {
			n++
		}
	}
	return n
}

// Stats.computeGapToCap: distance from the base value to a cap, with haste
// percentages divided by the matching speed multiplier.
func bulkGapToCap(baseStats bulkUnitStats, stat proto.Stat, pseudo proto.PseudoStat, isPseudo bool, cap float64) float64 {
	var delta float64
	if isPseudo {
		delta = cap - baseStats.pseudo[pseudo]
		switch pseudo {
		case proto.PseudoStat_PseudoStatMeleeHastePercent:
			delta /= baseStats.pseudo[proto.PseudoStat_PseudoStatMeleeSpeedMultiplier]
		case proto.PseudoStat_PseudoStatRangedHastePercent:
			delta /= baseStats.pseudo[proto.PseudoStat_PseudoStatRangedSpeedMultiplier]
		case proto.PseudoStat_PseudoStatSpellHastePercent:
			delta /= baseStats.pseudo[proto.PseudoStat_PseudoStatCastSpeedMultiplier]
		}
	} else {
		delta = cap - baseStats.stats[stat]
	}
	if delta == 0 || math.IsNaN(delta) || math.IsInf(delta, 0) {
		return bulkGemZeroGap
	}
	return delta
}

// Stats.computeStatCapsDelta.
func (o *bulkGemOptimizer) capGaps(baseStats bulkUnitStats) bulkUnitStats {
	gaps := newBulkUnitStats()
	for stat, cap := range o.statCaps.stats {
		if cap > 0 {
			gaps.stats[stat] = bulkGapToCap(baseStats, proto.Stat(stat), 0, false, cap)
		}
	}
	for pseudo, cap := range o.statCaps.pseudo {
		if cap > 0 {
			gaps.pseudo[pseudo] = bulkGapToCap(baseStats, 0, proto.PseudoStat(pseudo), true, cap)
		}
	}
	return gaps
}

// computeReforgeSoftCaps.
func (o *bulkGemOptimizer) relativeSoftCaps(baseStats bulkUnitStats) []bulkSoftCap {
	var result []bulkSoftCap
	for _, config := range o.softCaps {
		cap := bulkSoftCap{capType: config.CapType, postCapEPs: slices.Clone(config.PostCapEps)}
		switch target := config.UnitStat.(type) {
		case *proto.BulkStatCap_Stat:
			cap.stat = target.Stat
		case *proto.BulkStatCap_PseudoStat:
			cap.pseudoStat, cap.isPseudo = target.PseudoStat, true
		default:
			continue
		}
		for _, bp := range config.Breakpoints {
			cap.breaks = append(cap.breaks, bulkGapToCap(baseStats, cap.stat, cap.pseudoStat, cap.isPseudo, bp))
		}
		if cap.capType == proto.BulkStatCapType_BulkStatCapTypeThreshold {
			slices.Reverse(cap.breaks)
			first := 0.0
			if len(cap.postCapEPs) > 0 {
				first = cap.postCapEPs[0]
			}
			cap.postCapEPs = slices.Repeat([]float64{first}, len(cap.breaks))
		}
		result = append(result, cap)
	}
	return result
}

// ReforgeOptimizer.checkWeights: reconcile parent rating weights with child
// percent weights and caps.
func bulkCheckWeights(weights bulkUnitStats, caps bulkUnitStats, softCaps []bulkSoftCap) bulkUnitStats {
	validated := weights.clone()
	for _, parent := range []proto.Stat{
		proto.Stat_StatMeleeHitRating, proto.Stat_StatSpellHitRating, proto.Stat_StatMeleeCritRating, proto.Stat_StatSpellCritRating,
		proto.Stat_StatMeleeHasteRating, proto.Stat_StatSpellHasteRating, proto.Stat_StatDefenseRating, proto.Stat_StatResilienceRating,
	} {
		children := bulkChildPseudoStats(parent)
		anyChildWeighted := false
		for _, child := range children {
			if parent == proto.Stat_StatSpellHitRating && slices.Contains(bulkSchoolHitPseudoStats, child) {
				continue
			}
			if validated.pseudo[child] != 0 {
				anyChildWeighted = true
				break
			}
		}
		if anyChildWeighted {
			validated.stats[parent] = 0
			continue
		}
		for _, child := range children {
			if bulkPseudoHasCap(child, caps, softCaps) {
				if factor, ok := bulkChildPercentToRating(parent, child); ok {
					validated.pseudo[child] += validated.stats[parent] * factor
					validated.stats[parent] = 0
				}
				break
			}
		}
	}
	return validated
}

// A gem option for a socket color on one candidate: buildGemOptions output.
type bulkGemData struct {
	gem    Gem
	isJC   bool
	coeffs bulkCoeffs
	score  float64
}

// weights are the validated (cap-reconciled) weights, as in optimizeReforges.
func (o *bulkGemOptimizer) gemOptions(weights bulkUnitStats, caps bulkUnitStats, softCaps []bulkSoftCap) map[proto.GemColor][]bulkGemData {
	options := map[proto.GemColor][]bulkGemData{}
	for _, color := range []proto.GemColor{proto.GemColor_GemColorPrismatic, proto.GemColor_GemColorRed, proto.GemColor_GemColorBlue, proto.GemColor_GemColorYellow} {
		var filtered []bulkGemData
		for _, e := range o.eligibleByColor[color] {
			coeffs := newBulkCoeffs()
			for stat, value := range e.gem.Stats {
				if value != 0 {
					o.applyStat(coeffs, proto.Stat(stat), value, weights)
				}
			}
			filtered = append(filtered, bulkGemData{gem: e.gem, isJC: e.isJC, coeffs: coeffs, score: coeffs.score(weights)})
		}
		sort.SliceStable(filtered, func(a, b int) bool { return filtered[a].score > filtered[b].score })

		var included []bulkGemData
		foundUncappedJC, foundUncappedNormal := false, false
		for _, data := range filtered {
			capped := bulkCoeffsCappedCount(data.coeffs, caps, softCaps)
			if (!data.isJC || !foundUncappedJC) && (capped == 0 || !foundUncappedNormal) {
				included = append(included, data)
			}
			if capped == 0 {
				if data.isJC {
					foundUncappedJC = true
				} else {
					foundUncappedNormal = true
				}
			}
		}
		options[color] = included
	}
	return options
}

// The model for one candidate: socket variables grouped by item, optional
// socket bonus per item, and the global constraints.
type bulkSocketOption struct {
	gem         Gem
	coeffs      bulkCoeffs
	unique      bool
	matches     bool // gem matches the socket color (feeds the socket bonus link)
	metaColors  []proto.GemColor
	compareDiff int
}

type bulkSocketModel struct {
	slot      proto.ItemSlot
	socketIdx int
	options   []bulkSocketOption
}

type bulkItemModel struct {
	slot    proto.ItemSlot
	sockets []int // indexes into model.sockets
	// Socket bonus as an optional choice, present when not forced into the gems.
	hasBonus    bool
	bonusCoeffs bulkCoeffs
}

type bulkGemModel struct {
	sockets []bulkSocketModel
	items   []bulkItemModel
	// Meta gem requirements remaining after gems in frozen slots.
	minColor       map[proto.GemColor]int
	compareGreater proto.GemColor
	compareLesser  proto.GemColor
	minCompare     int
}

// Cap constraints added while iterating: a stat's total contribution must be
// >= or <= the value.
type bulkStatConstraintSet struct {
	statGE, statLE     map[proto.Stat]float64
	pseudoGE, pseudoLE map[proto.PseudoStat]float64
}

func newBulkStatConstraintSet() bulkStatConstraintSet {
	return bulkStatConstraintSet{map[proto.Stat]float64{}, map[proto.Stat]float64{}, map[proto.PseudoStat]float64{}, map[proto.PseudoStat]float64{}}
}

func (s bulkStatConstraintSet) hasStat(stat proto.Stat) bool {
	_, ge := s.statGE[stat]
	_, le := s.statLE[stat]
	return ge || le
}

func (s bulkStatConstraintSet) hasPseudo(p proto.PseudoStat) bool {
	_, ge := s.pseudoGE[p]
	_, le := s.pseudoLE[p]
	return ge || le
}

// buildYalpsVariables + buildYalpsConstraints.
func (o *bulkGemOptimizer) buildModel(gear *Equipment, options map[proto.GemColor][]bulkGemData, weights bulkUnitStats, caps bulkUnitStats, softCaps []bulkSoftCap) *bulkGemModel {
	model := &bulkGemModel{minColor: map[proto.GemColor]int{}}

	// Meta gem condition, net of gems already socketed (frozen slots).
	if metaGem := bulkFindMetaGem(gear); metaGem != nil {
		if cond, ok := o.metaConditions[metaGem.ID]; ok {
			fixed := map[proto.GemColor]int{}
			for slot := range gear {
				for _, gem := range gear[slot].Gems {
					if gem.ID == 0 || gem.Color == proto.GemColor_GemColorMeta {
						continue
					}
					for _, color := range bulkGemMetaColors(gem.Color) {
						fixed[color]++
					}
				}
			}
			if cond.CompareColorGreater != proto.GemColor_GemColorUnknown && cond.CompareColorLesser != proto.GemColor_GemColorUnknown {
				if remaining := 1 - (fixed[cond.CompareColorGreater] - fixed[cond.CompareColorLesser]); remaining > 0 {
					model.compareGreater, model.compareLesser, model.minCompare = cond.CompareColorGreater, cond.CompareColorLesser, remaining
				}
			}
			for color, minimum := range map[proto.GemColor]int32{proto.GemColor_GemColorBlue: cond.MinBlue, proto.GemColor_GemColorRed: cond.MinRed, proto.GemColor_GemColorYellow: cond.MinYellow} {
				if remaining := int(minimum) - fixed[color]; remaining > 0 {
					model.minColor[color] = remaining
				}
			}
		}
	}

	primary := []proto.GemColor{proto.GemColor_GemColorRed, proto.GemColor_GemColorBlue, proto.GemColor_GemColorYellow, proto.GemColor_GemColorPrismatic}
	for slotIdx := range gear {
		slot := proto.ItemSlot(slotIdx)
		item := &gear[slot]
		if item.ID == 0 || len(item.GemSockets) == 0 || o.frozenSlots[slot] {
			continue
		}
		socketColors := item.GemSockets
		normalization := len(socketColors)
		if normalization > 1 && socketColors[0] == proto.GemColor_GemColorMeta {
			normalization--
		}
		if normalization == 0 {
			normalization = 1
		}
		positiveBonus := map[proto.Stat]float64{}
		for stat, value := range item.SocketBonus {
			if value > 0 {
				positiveBonus[proto.Stat(stat)] = value
			}
		}
		distributed := map[proto.Stat]float64{}
		for stat, value := range positiveBonus {
			distributed[stat] = value / float64(normalization)
		}

		// Decide whether matching the socket bonus is a foregone conclusion.
		forceBonus := false
		bonusAsCoeff := newBulkCoeffs()
		for stat, value := range distributed {
			if o.undershootCaps.stats[stat] != 0 {
				continue
			}
			skip := false
			for _, child := range bulkChildPseudoStats(stat) {
				if o.undershootCaps.pseudo[child] != 0 {
					skip = true
				}
			}
			if skip {
				continue
			}
			o.applyStat(bonusAsCoeff, stat, value, weights)
		}
		if !bonusAsCoeff.empty() {
			withCap := bulkCoeffsIncludeStatWithCap(bonusAsCoeff, caps, softCaps)
			cappedAlready := bulkCoeffsIncludeCappedStat(bonusAsCoeff, caps)
			if withCap && !cappedAlready && normalization > 1 {
				forceBonus = true
			}
			matched, unmatched := newBulkCoeffs(), newBulkCoeffs()
			for _, socketColor := range socketColors {
				if !slices.Contains(primary, socketColor) {
					continue
				}
				if best := options[socketColor]; len(best) > 0 {
					matched.addAll(best[0].coeffs)
				}
				matched.addAll(bonusAsCoeff)
				if best := options[proto.GemColor_GemColorPrismatic]; len(best) > 0 {
					unmatched.addAll(best[0].coeffs)
				}
			}
			if matched.score(weights) > unmatched.score(weights) && (normalization > 1 || (withCap && !cappedAlready)) {
				forceBonus = true
			}
		}

		itemModel := bulkItemModel{slot: slot}
		for socketIdx, socketColor := range socketColors {
			var colorKeys []proto.GemColor
			switch {
			case socketColor == proto.GemColor_GemColorPrismatic:
				colorKeys = []proto.GemColor{socketColor}
			case socketColor == proto.GemColor_GemColorRed || socketColor == proto.GemColor_GemColorBlue || socketColor == proto.GemColor_GemColorYellow:
				colorKeys = []proto.GemColor{socketColor}
				if !forceBonus {
					colorKeys = append(colorKeys, proto.GemColor_GemColorPrismatic)
				}
			default:
				continue
			}
			socket := bulkSocketModel{slot: slot, socketIdx: socketIdx}
			for _, colorKey := range colorKeys {
				for _, data := range options[colorKey] {
					if slices.ContainsFunc(socket.options, func(existing bulkSocketOption) bool { return existing.gem.ID == data.gem.ID }) {
						continue
					}
					option := bulkSocketOption{gem: data.gem, coeffs: data.coeffs.clone(), unique: data.gem.Unique, metaColors: bulkGemMetaColors(data.gem.Color)}
					if model.compareGreater != proto.GemColor_GemColorUnknown {
						if slices.Contains(option.metaColors, model.compareGreater) {
							option.compareDiff++
						}
						if slices.Contains(option.metaColors, model.compareLesser) {
							option.compareDiff--
						}
					}
					if bulkGemColorMatchesSocket(data.gem.Color, socketColor) {
						option.matches = true
						if forceBonus {
							for stat, value := range distributed {
								o.applyStat(option.coeffs, stat, value, weights)
							}
						}
					}
					socket.options = append(socket.options, option)
				}
			}
			itemModel.sockets = append(itemModel.sockets, len(model.sockets))
			model.sockets = append(model.sockets, socket)
		}
		if !forceBonus && len(itemModel.sockets) > 0 {
			itemModel.hasBonus = true
			itemModel.bonusCoeffs = newBulkCoeffs()
			for stat, value := range positiveBonus {
				o.applyStat(itemModel.bonusCoeffs, stat, value, weights)
			}
		}
		if len(itemModel.sockets) > 0 {
			model.items = append(model.items, itemModel)
		}
	}
	return model
}

func bulkFindMetaGem(gear *Equipment) *Gem {
	for slot := range gear {
		for idx := range gear[slot].Gems {
			if gear[slot].Gems[idx].ID != 0 && gear[slot].Gems[idx].Color == proto.GemColor_GemColorMeta {
				return &gear[slot].Gems[idx]
			}
		}
	}
	return nil
}

// A solution: chosen option per socket (-1 for none) and bonus per item.
type bulkGemSolution struct {
	choice   []int
	bonus    []bool
	score    float64
	feasible bool
}

// Exact branch-and-bound over the sockets: options are tried best-first, the
// remaining sockets' best scores bound the search, "<=" constraints prune as
// soon as they are exceeded (every coefficient is non-negative) and ">="
// constraints prune once they can no longer be met.
type bulkGemSolver struct {
	model       *bulkGemModel
	weights     bulkUnitStats
	constraints bulkStatConstraintSet
	// Per socket: options ordered by score, with scores; per item: bonus score.
	order       [][]int
	scores      [][]float64
	bonusScore  []float64
	socketItem  []int     // item index per socket
	itemLast    []int     // last socket index of each item (bonus decided there)
	restBound   []float64 // best possible score from socket i onward
	restMaxStat map[proto.Stat][]float64
	restMaxPsd  map[proto.PseudoStat][]float64
	restColor   map[proto.GemColor][]int
	restCompare []int
	nodes       int
	best        bulkGemSolution
	// Search state.
	choice     []int
	bonus      []bool
	statSum    bulkUnitStats
	colorCount map[proto.GemColor]int
	compareSum int
	uniqueUsed map[int32]int
	matchedAll []bool
}

func newBulkGemSolver(model *bulkGemModel, weights bulkUnitStats, constraints bulkStatConstraintSet) *bulkGemSolver {
	n := len(model.sockets)
	s := &bulkGemSolver{
		model: model, weights: weights, constraints: constraints,
		order: make([][]int, n), scores: make([][]float64, n), bonusScore: make([]float64, len(model.items)),
		socketItem: make([]int, n), itemLast: make([]int, len(model.items)), restBound: make([]float64, n+1),
		restMaxStat: map[proto.Stat][]float64{}, restMaxPsd: map[proto.PseudoStat][]float64{}, restColor: map[proto.GemColor][]int{}, restCompare: make([]int, n+1),
		choice: make([]int, n), bonus: make([]bool, len(model.items)), statSum: newBulkUnitStats(), colorCount: map[proto.GemColor]int{}, uniqueUsed: map[int32]int{},
		matchedAll: make([]bool, len(model.items)),
	}
	for itemIdx, item := range model.items {
		s.bonusScore[itemIdx] = 0
		if item.hasBonus {
			s.bonusScore[itemIdx] = item.bonusCoeffs.score(weights)
		}
		for _, socketIdx := range item.sockets {
			s.socketItem[socketIdx] = itemIdx
		}
		if len(item.sockets) > 0 {
			s.itemLast[itemIdx] = item.sockets[len(item.sockets)-1]
		}
	}
	for i, socket := range model.sockets {
		s.scores[i] = make([]float64, len(socket.options))
		for j, option := range socket.options {
			s.scores[i][j] = option.coeffs.score(weights)
		}
		s.order[i] = s.nonDominatedOptions(i)
		sort.SliceStable(s.order[i], func(a, b int) bool { return s.scores[i][s.order[i][a]] > s.scores[i][s.order[i][b]] })
	}
	// Bounds from the end: best score and best contribution per constrained stat/color.
	for stat := range constraints.statGE {
		s.restMaxStat[stat] = make([]float64, n+1)
	}
	for p := range constraints.pseudoGE {
		s.restMaxPsd[p] = make([]float64, n+1)
	}
	for color := range model.minColor {
		s.restColor[color] = make([]int, n+1)
	}
	for i := n - 1; i >= 0; i-- {
		bestScore := 0.0
		for _, j := range s.order[i] {
			option := model.sockets[i].options[j]
			bestScore = math.Max(bestScore, s.scores[i][j])
			for stat := range s.restMaxStat {
				s.restMaxStat[stat][i] = math.Max(s.restMaxStat[stat][i], option.coeffs.stats[stat])
			}
			for p := range s.restMaxPsd {
				s.restMaxPsd[p][i] = math.Max(s.restMaxPsd[p][i], option.coeffs.pseudo[p])
			}
			for color := range s.restColor {
				if slices.Contains(option.metaColors, color) {
					s.restColor[color][i] = 1
				}
			}
			if option.compareDiff > s.restCompare[i] {
				s.restCompare[i] = option.compareDiff
			}
		}
		itemIdx := s.socketItem[i]
		if s.itemLast[itemIdx] == i && model.items[itemIdx].hasBonus {
			bestScore += math.Max(0, s.bonusScore[itemIdx])
			for stat := range s.restMaxStat {
				s.restMaxStat[stat][i] += model.items[itemIdx].bonusCoeffs.stats[stat]
			}
			for p := range s.restMaxPsd {
				s.restMaxPsd[p][i] += model.items[itemIdx].bonusCoeffs.pseudo[p]
			}
		}
		s.restBound[i] = s.restBound[i+1] + bestScore
		for stat := range s.restMaxStat {
			s.restMaxStat[stat][i] += s.restMaxStat[stat][i+1]
		}
		for p := range s.restMaxPsd {
			s.restMaxPsd[p][i] += s.restMaxPsd[p][i+1]
		}
		for color := range s.restColor {
			s.restColor[color][i] += s.restColor[color][i+1]
		}
		s.restCompare[i] += s.restCompare[i+1]
	}
	for i := range s.choice {
		s.choice[i] = -1
	}
	s.best = bulkGemSolution{score: math.Inf(-1)}
	return s
}

// The options of a socket that no other option of the same socket makes
// redundant. Option a dominates b when a is not unique, scores at least as
// well, matches the socket whenever b does, gives at least as much of every
// lower-bounded stat and color, no more of any upper-bounded stat, and at least
// as much toward the meta compare rule. Any solution using b can use a instead
// without losing score or feasibility, so b is never needed.
func (s *bulkGemSolver) nonDominatedOptions(i int) []int {
	options := s.model.sockets[i].options
	dominates := func(a, b int) bool {
		oa, ob := &options[a], &options[b]
		if oa.unique || s.scores[i][a] < s.scores[i][b]-bulkScoreEpsilon {
			return false
		}
		if ob.matches && !oa.matches {
			return false
		}
		for stat := range s.constraints.statGE {
			if oa.coeffs.stats[stat] < ob.coeffs.stats[stat]-bulkScoreEpsilon {
				return false
			}
		}
		for p := range s.constraints.pseudoGE {
			if oa.coeffs.pseudo[p] < ob.coeffs.pseudo[p]-bulkScoreEpsilon {
				return false
			}
		}
		for stat := range s.constraints.statLE {
			if oa.coeffs.stats[stat] > ob.coeffs.stats[stat]+bulkScoreEpsilon {
				return false
			}
		}
		for p := range s.constraints.pseudoLE {
			if oa.coeffs.pseudo[p] > ob.coeffs.pseudo[p]+bulkScoreEpsilon {
				return false
			}
		}
		for color := range s.model.minColor {
			if slices.Contains(ob.metaColors, color) && !slices.Contains(oa.metaColors, color) {
				return false
			}
		}
		if s.model.minCompare > 0 && oa.compareDiff < ob.compareDiff {
			return false
		}
		return true
	}
	var kept []int
	for b := range options {
		dominated := false
		for a := range options {
			if a == b || !dominates(a, b) {
				continue
			}
			// Mutual dominance (identical options): keep the earlier one only.
			if dominates(b, a) && a > b {
				continue
			}
			dominated = true
			break
		}
		if !dominated {
			kept = append(kept, b)
		}
	}
	return kept
}

func (s *bulkGemSolver) exceedsUpperBounds() bool {
	for stat, limit := range s.constraints.statLE {
		if s.statSum.stats[stat] > limit+bulkScoreEpsilon {
			return true
		}
	}
	for p, limit := range s.constraints.pseudoLE {
		if s.statSum.pseudo[p] > limit+bulkScoreEpsilon {
			return true
		}
	}
	return false
}

// Whether the ">=" constraints can still be met from socket i onward.
func (s *bulkGemSolver) lowerBoundsReachable(i int) bool {
	for stat, need := range s.constraints.statGE {
		if s.statSum.stats[stat]+s.restMaxStat[stat][i] < need-bulkScoreEpsilon {
			return false
		}
	}
	for p, need := range s.constraints.pseudoGE {
		if s.statSum.pseudo[p]+s.restMaxPsd[p][i] < need-bulkScoreEpsilon {
			return false
		}
	}
	for color, need := range s.model.minColor {
		if s.colorCount[color]+s.restColor[color][i] < need {
			return false
		}
	}
	if s.model.minCompare > 0 && s.compareSum+s.restCompare[i] < s.model.minCompare {
		return false
	}
	return true
}

func (s *bulkGemSolver) addCoeffs(c bulkCoeffs, sign float64) {
	for stat, v := range c.stats {
		s.statSum.stats[stat] += sign * v
	}
	for p, v := range c.pseudo {
		s.statSum.pseudo[p] += sign * v
	}
}

func (s *bulkGemSolver) solve() bulkGemSolution {
	s.search(0, 0)
	if math.IsInf(s.best.score, -1) {
		return bulkGemSolution{}
	}
	s.best.feasible = true
	return s.best
}

func (s *bulkGemSolver) record(score float64) {
	if score > s.best.score+bulkScoreEpsilon {
		s.best = bulkGemSolution{choice: slices.Clone(s.choice), bonus: slices.Clone(s.bonus), score: score}
	}
}

// Explores socket i with the given score so far.
func (s *bulkGemSolver) search(i int, score float64) {
	s.nodes++
	if s.nodes > bulkGemSolverNodeBudget {
		return
	}
	if score+s.restBound[i] <= s.best.score+bulkScoreEpsilon {
		return
	}
	if !s.lowerBoundsReachable(i) {
		return
	}
	if i == len(s.model.sockets) {
		s.record(score)
		return
	}
	socket := &s.model.sockets[i]
	itemIdx := s.socketItem[i]
	last := s.itemLast[itemIdx] == i

	tryOption := func(optionIdx int, optionScore float64, matches bool) {
		var option *bulkSocketOption
		if optionIdx >= 0 {
			option = &socket.options[optionIdx]
			if option.unique && s.uniqueUsed[option.gem.ID] > 0 {
				return
			}
			s.addCoeffs(option.coeffs, 1)
			if s.exceedsUpperBounds() {
				s.addCoeffs(option.coeffs, -1)
				return
			}
			for _, color := range option.metaColors {
				s.colorCount[color]++
			}
			s.compareSum += option.compareDiff
			if option.unique {
				s.uniqueUsed[option.gem.ID]++
			}
		}
		s.choice[i] = optionIdx

		// Every socket of the item matched so far?
		prevMatched := s.matchedAll[itemIdx]
		if s.model.sockets[i].socketIdx == 0 || s.model.items[itemIdx].sockets[0] == i {
			s.matchedAll[itemIdx] = matches
		} else {
			s.matchedAll[itemIdx] = prevMatched && matches
		}

		if last && s.model.items[itemIdx].hasBonus && s.matchedAll[itemIdx] {
			// Bonus is an independent binary here: take it when it helps and stays feasible.
			bonus := s.model.items[itemIdx].bonusCoeffs
			s.addCoeffs(bonus, 1)
			if !s.exceedsUpperBounds() {
				s.bonus[itemIdx] = true
				s.search(i+1, score+optionScore+s.bonusScore[itemIdx])
			}
			s.addCoeffs(bonus, -1)
			s.bonus[itemIdx] = false
			// Also without the bonus, in case a lower bound elsewhere prefers it off
			// (never better in score, but kept for constraint completeness).
			if s.bonusScore[itemIdx] < 0 || len(s.constraints.statLE)+len(s.constraints.pseudoLE) > 0 {
				s.search(i+1, score+optionScore)
			}
		} else {
			s.bonus[itemIdx] = false
			s.search(i+1, score+optionScore)
		}

		s.matchedAll[itemIdx] = prevMatched
		s.choice[i] = -1
		if option != nil {
			if option.unique {
				s.uniqueUsed[option.gem.ID]--
			}
			s.compareSum -= option.compareDiff
			for _, color := range option.metaColors {
				s.colorCount[color]--
			}
			s.addCoeffs(option.coeffs, -1)
		}
	}

	for _, optionIdx := range s.order[i] {
		tryOption(optionIdx, s.scores[i][optionIdx], socket.options[optionIdx].matches)
	}
	// Leaving the socket empty.
	tryOption(-1, 0, false)
}

// Total stat contribution of a solution, for cap checks.
func bulkSolutionContribution(model *bulkGemModel, solution bulkGemSolution) bulkUnitStats {
	total := newBulkUnitStats()
	add := func(c bulkCoeffs) {
		for stat, v := range c.stats {
			total.stats[stat] += v
		}
		for p, v := range c.pseudo {
			total.pseudo[p] += v
		}
	}
	for i, choice := range solution.choice {
		if choice >= 0 {
			add(model.sockets[i].options[choice].coeffs)
		}
	}
	for itemIdx, on := range solution.bonus {
		if on {
			add(model.items[itemIdx].bonusCoeffs)
		}
	}
	return total
}

// solveModel + checkCaps: solve, then keep adding cap constraints and
// post-cap weights until no unconstrained cap is exceeded.
// Returns the solution and the weights in force when it was found.
func (o *bulkGemOptimizer) solveWithCaps(model *bulkGemModel, weights bulkUnitStats, caps bulkUnitStats, softCaps []bulkSoftCap) (bulkGemSolution, bulkUnitStats, error) {
	constraints := newBulkStatConstraintSet()
	softRemaining := make([]bulkSoftCap, len(softCaps))
	for i := range softCaps {
		softRemaining[i] = bulkSoftCap{stat: softCaps[i].stat, pseudoStat: softCaps[i].pseudoStat, isPseudo: softCaps[i].isPseudo, capType: softCaps[i].capType, breaks: slices.Clone(softCaps[i].breaks), postCapEPs: slices.Clone(softCaps[i].postCapEPs)}
	}
	for iteration := 0; iteration < 64; iteration++ {
		solver := newBulkGemSolver(model, weights, constraints)
		solution := solver.solve()
		if !solution.feasible {
			return solution, weights, fmt.Errorf("%s", bulkCapInfeasibleMessage)
		}
		contribution := bulkSolutionContribution(model, solution)

		exceeded := false
		for stat := 0; stat < bulkNumStats; stat++ {
			cap := caps.stats[stat]
			value := contribution.stats[stat]
			if cap != 0 && value > cap && !constraints.hasStat(proto.Stat(stat)) {
				exceeded = true
				if o.undershootCaps.stats[stat] != 0 {
					constraints.statLE[proto.Stat(stat)] = cap
				} else {
					constraints.statGE[proto.Stat(stat)] = cap
					weights = weights.clone()
					weights.stats[stat] = 0
				}
			}
		}
		for p := 0; p < bulkNumPseudoStats; p++ {
			cap := caps.pseudo[p]
			value := contribution.pseudo[p]
			if cap != 0 && value > cap && !constraints.hasPseudo(proto.PseudoStat(p)) {
				exceeded = true
				if o.undershootCaps.pseudo[p] != 0 {
					constraints.pseudoLE[proto.PseudoStat(p)] = cap
				} else {
					constraints.pseudoGE[proto.PseudoStat(p)] = cap
					weights = weights.clone()
					weights.pseudo[p] = 0
				}
			}
		}

		for !exceeded && len(softRemaining) > 0 {
			next := &softRemaining[0]
			var current float64
			if next.isPseudo {
				current = contribution.pseudo[next.pseudoStat]
			} else {
				current = contribution.stats[next.stat]
			}
			idx := 0
			for _, bp := range next.breaks {
				if current > bp {
					weights = weights.clone()
					if next.isPseudo {
						constraints.pseudoGE[next.pseudoStat] = bp
						weights.pseudo[next.pseudoStat] = next.postCapEPs[idx]
					} else {
						constraints.statGE[next.stat] = bp
						weights.stats[next.stat] = next.postCapEPs[idx]
					}
					exceeded = true
					break
				}
				idx++
			}
			if next.capType == proto.BulkStatCapType_BulkStatCapTypeSoftCap {
				cut := min(idx+1, len(next.breaks))
				next.breaks = next.breaks[cut:]
				next.postCapEPs = next.postCapEPs[min(cut, len(next.postCapEPs)):]
			}
			if next.capType == proto.BulkStatCapType_BulkStatCapTypeThreshold || len(next.breaks) == 0 {
				softRemaining = softRemaining[1:]
			}
		}

		if !exceeded {
			return solution, weights, nil
		}
	}
	return bulkGemSolution{}, weights, fmt.Errorf("gem optimization did not converge")
}

// Gears the candidate with the solution's gems, then moves gems around so as
// few sockets as possible change from the original placement (minimizeRegems).
func (o *bulkGemOptimizer) applySolution(cleared Equipment, original *Equipment, model *bulkGemModel, solution bulkGemSolution) Equipment {
	gear := cleared
	for i, choice := range solution.choice {
		if choice < 0 {
			continue
		}
		socket := model.sockets[i]
		gear = bulkWithGem(gear, socket.slot, socket.socketIdx, socket.options[choice].gem)
	}
	return o.minimizeRegems(gear, original)
}

func (o *bulkGemOptimizer) minimizeRegems(gear Equipment, original *Equipment) Equipment {
	type socketKey struct {
		slot proto.ItemSlot
		idx  int
	}
	finalized := map[socketKey]bool{}
	for slotIdx := range gear {
		slot := proto.ItemSlot(slotIdx)
		newItem, originalItem := gear[slot], original[slot]
		if newItem.ID == 0 || originalItem.ID == 0 {
			continue
		}
		for socketIdx, socketColor := range newItem.GemSockets {
			key := socketKey{slot, socketIdx}
			if finalized[key] {
				continue
			}
			finalized[key] = true
			var newGem, originalGem Gem
			if socketIdx < len(newItem.Gems) {
				newGem = newItem.Gems[socketIdx]
			}
			if socketIdx < len(originalItem.Gems) {
				originalGem = originalItem.Gems[socketIdx]
			}
			if newGem.ID == 0 || originalGem.ID == 0 || newGem.ID == originalGem.ID {
				continue
			}
			if bulkGemColorMatchesSocket(newGem.Color, socketColor) && !bulkGemColorMatchesSocket(originalGem.Color, socketColor) {
				continue
			}
			// Where does the original gem live now? Swap it back here if the
			// swap loses nothing.
			for matchSlotIdx := range gear {
				matchSlot := proto.ItemSlot(matchSlotIdx)
				if o.frozenSlots[matchSlot] {
					continue
				}
				swapped := false
				for matchIdx, gem := range gear[matchSlot].Gems {
					if gem.ID != originalGem.ID {
						continue
					}
					matchKey := socketKey{matchSlot, matchIdx}
					if finalized[matchKey] {
						continue
					}
					matchColor := gear[matchSlot].GemSockets[matchIdx]
					if bulkGemColorMatchesSocket(originalGem.Color, matchColor) && !bulkGemColorMatchesSocket(newGem.Color, matchColor) {
						continue
					}
					finalized[matchKey] = true
					gear = bulkWithGem(gear, slot, socketIdx, originalGem)
					gear = bulkWithGem(gear, matchSlot, matchIdx, newGem)
					swapped = true
					break
				}
				if swapped {
					break
				}
			}
		}
	}
	return gear
}

// Gear.withoutGems(frozenSlots, true): clear every non-frozen socket, keeping the meta gem.
func (o *bulkGemOptimizer) clearGems(gear *Equipment) Equipment {
	cleared := *gear
	var metaGem *Gem
	if found := bulkFindMetaGem(gear); found != nil {
		copied := *found
		metaGem = &copied
	}
	for slotIdx := range cleared {
		slot := proto.ItemSlot(slotIdx)
		if cleared[slot].ID == 0 || o.frozenSlots[slot] {
			continue
		}
		item := bulkCloneItem(cleared[slot])
		item.Gems = make([]Gem, len(item.GemSockets))
		cleared[slot] = item
	}
	if metaGem != nil {
		head := &cleared[proto.ItemSlot_ItemSlotHead]
		for socketIdx, color := range head.GemSockets {
			if color == proto.GemColor_GemColorMeta {
				cleared = bulkWithGem(cleared, proto.ItemSlot_ItemSlotHead, socketIdx, *metaGem)
				break
			}
		}
	}
	return cleared
}

// Optimizes one candidate's gems. Mirrors optimizeReforges(gear, batchRun=true).
func (o *bulkGemOptimizer) optimize(candidate *Equipment) (Equipment, error) {
	cleared := o.clearGems(candidate)
	finalStats, err := bulkFinalStats(o.base, cleared.ToEquipmentSpecProto())
	if err != nil {
		return *candidate, err
	}
	baseStats := bulkUnitStatsFromProto(finalStats)
	for i, v := range o.debuffStats.stats {
		baseStats.stats[i] += v
	}
	for i, v := range o.debuffStats.pseudo {
		baseStats.pseudo[i] += v
	}

	caps := o.capGaps(baseStats)
	softCaps := o.relativeSoftCaps(baseStats)
	weights := bulkCheckWeights(o.preCapEPs, caps, softCaps)
	options := o.gemOptions(weights, caps, softCaps)
	model := o.buildModel(&cleared, options, weights, caps, softCaps)
	if len(model.sockets) == 0 {
		return cleared, nil
	}
	solution, _, err := o.solveWithCaps(model, weights, caps, softCaps)
	if err != nil {
		return *candidate, err
	}
	return o.applySolution(cleared, candidate, model, solution), nil
}

func init() {
	bulkOptimizeGems = func(request *proto.BulkSimRequest, player *proto.Player, candidates []*bulkCandidate, workers int, aborted func() bool, onProgress func(done int)) error {
		optimizer, err := newBulkGemOptimizer(request, player)
		if err != nil {
			return err
		}
		return bulkRunParallel(len(candidates), workers, aborted, func(i int) error {
			gear, err := optimizer.optimize(&candidates[i].gear)
			if err != nil {
				return err
			}
			candidates[i].gear = gear
			return nil
		}, onProgress)
	}
}
