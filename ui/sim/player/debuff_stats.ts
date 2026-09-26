import { Debuffs, PseudoStat, Stat, TristateEffect } from '@generated/proto/common';

import { Stats } from '../proto/stats';

/**
 * What the stats panel adds to a character's final stats for the raid's debuffs. They lower the
 * target's defences rather than raising the character's stats, so final stats leave them out, but
 * the panel shows them as the character's own, and batch stat constraints are judged on the
 * panel's values. Expose Weakness is credited `exposeWeaknessAgility`, which is the configured
 * hunter agility except for a hunter who applies it with their own talent. Mirrored in Go by
 * core.CharacterSheetDebuffStats (sim/core/character_sheet.go).
 */
export const characterSheetDebuffStats = (debuffs: Debuffs, exposeWeaknessAgility = debuffs.exposeWeaknessHunterAgility): Stats => {
	let debuffStats = new Stats();

	if (debuffs.faerieFire == TristateEffect.TristateEffectImproved) {
		debuffStats = debuffStats.addPseudoStat(PseudoStat.PseudoStatMeleeHitPercent, 3);
		debuffStats = debuffStats.addPseudoStat(PseudoStat.PseudoStatRangedHitPercent, 3);
	}

	if (debuffs.improvedSealOfTheCrusader) {
		debuffStats = debuffStats.addPseudoStat(PseudoStat.PseudoStatMeleeCritPercent, 3);
		debuffStats = debuffStats.addPseudoStat(PseudoStat.PseudoStatRangedCritPercent, 3);
		debuffStats = debuffStats.addPseudoStat(PseudoStat.PseudoStatSpellCritPercent, 3);
	}

	if (debuffs.exposeWeaknessUptime && debuffs.exposeWeaknessHunterAgility) {
		debuffStats = debuffStats.addStat(Stat.StatAttackPower, exposeWeaknessAgility * 0.25);
		debuffStats = debuffStats.addStat(Stat.StatRangedAttackPower, exposeWeaknessAgility * 0.25);
	}

	if (debuffs.huntersMark != TristateEffect.TristateEffectMissing) {
		debuffStats = debuffStats.addStat(Stat.StatRangedAttackPower, 440);

		if (debuffs.huntersMark == TristateEffect.TristateEffectImproved) {
			debuffStats = debuffStats.addStat(Stat.StatAttackPower, 110);
		}
	}

	return debuffStats;
};
