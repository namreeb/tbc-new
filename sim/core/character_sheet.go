package core

import (
	"github.com/wowsims/tbc/sim/core/proto"
	"github.com/wowsims/tbc/sim/core/stats"
)

// CharacterSheetDebuffStats returns what the stats panel adds to a character's final stats for the
// raid's debuffs. They lower the target's defences rather than raising the character's stats, so
// FinalStats leaves them out, but the panel shows them as the character's own, and batch stat
// constraints and the gem optimizer's caps are judged on the panel's values. Mirrors
// characterSheetDebuffStats in ui/sim/player/debuff_stats.ts, except that a hunter who applies
// Expose Weakness with their own talent is credited the configured agility, not their own.
func CharacterSheetDebuffStats(debuffs *proto.Debuffs) UnitStats {
	result := NewUnitStats()
	if debuffs.GetFaerieFire() == proto.TristateEffect_TristateEffectImproved {
		result.AddStat(stats.UnitStatFromPseudoStat(proto.PseudoStat_PseudoStatMeleeHitPercent), 3)
		result.AddStat(stats.UnitStatFromPseudoStat(proto.PseudoStat_PseudoStatRangedHitPercent), 3)
	}
	if debuffs.GetImprovedSealOfTheCrusader() != proto.TristateEffect_TristateEffectMissing {
		result.AddStat(stats.UnitStatFromPseudoStat(proto.PseudoStat_PseudoStatMeleeCritPercent), 3)
		result.AddStat(stats.UnitStatFromPseudoStat(proto.PseudoStat_PseudoStatRangedCritPercent), 3)
		result.AddStat(stats.UnitStatFromPseudoStat(proto.PseudoStat_PseudoStatSpellCritPercent), 3)
	}
	if debuffs.GetExposeWeaknessUptime() != 0 && debuffs.GetExposeWeaknessHunterAgility() != 0 {
		attackPower := debuffs.GetExposeWeaknessHunterAgility() * 0.25
		result.AddStat(stats.UnitStatFromStat(stats.AttackPower), attackPower)
		result.AddStat(stats.UnitStatFromStat(stats.RangedAttackPower), attackPower)
	}
	if debuffs.GetHuntersMark() != proto.TristateEffect_TristateEffectMissing {
		result.AddStat(stats.UnitStatFromStat(stats.RangedAttackPower), 440)
		if debuffs.GetHuntersMark() == proto.TristateEffect_TristateEffectImproved {
			result.AddStat(stats.UnitStatFromStat(stats.AttackPower), 110)
		}
	}
	return result
}

// WithCharacterSheetDebuffs returns final stats as the stats panel shows them: with the raid's
// debuffs added (see CharacterSheetDebuffStats).
func WithCharacterSheetDebuffs(finalStats *proto.UnitStats, debuffs *proto.Debuffs) *proto.UnitStats {
	sheet := CharacterSheetDebuffStats(debuffs)
	result := &proto.UnitStats{
		Stats:       make([]float64, max(len(finalStats.GetStats()), len(sheet.Stats))),
		PseudoStats: make([]float64, max(len(finalStats.GetPseudoStats()), len(sheet.PseudoStats))),
	}
	copy(result.Stats, finalStats.GetStats())
	copy(result.PseudoStats, finalStats.GetPseudoStats())
	for i, value := range sheet.Stats {
		result.Stats[i] += value
	}
	for i, value := range sheet.PseudoStats {
		result.PseudoStats[i] += value
	}
	return result
}
