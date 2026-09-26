// The same table as sim/core/character_sheet_test.go: the TypeScript and Go copies of what the stats
// panel adds for the raid's debuffs must agree.
import { Debuffs, PseudoStat, Stat, TristateEffect } from '@generated/proto/common';
import { describe, expect, it } from 'vitest';

import { characterSheetDebuffStats } from './debuff_stats';

type Expected = { stats?: Partial<Record<Stat, number>>; pseudoStats?: Partial<Record<PseudoStat, number>> };

const cases: Array<[string, Partial<Debuffs>, Expected]> = [
	['none', {}, {}],
	[
		'improved faerie fire',
		{ faerieFire: TristateEffect.TristateEffectImproved },
		{ pseudoStats: { [PseudoStat.PseudoStatMeleeHitPercent]: 3, [PseudoStat.PseudoStatRangedHitPercent]: 3 } },
	],
	['regular faerie fire adds nothing', { faerieFire: TristateEffect.TristateEffectRegular }, {}],
	[
		'improved seal of the crusader',
		{ improvedSealOfTheCrusader: TristateEffect.TristateEffectRegular },
		{
			pseudoStats: {
				[PseudoStat.PseudoStatMeleeCritPercent]: 3,
				[PseudoStat.PseudoStatRangedCritPercent]: 3,
				[PseudoStat.PseudoStatSpellCritPercent]: 3,
			},
		},
	],
	[
		'expose weakness',
		{ exposeWeaknessUptime: 0.9, exposeWeaknessHunterAgility: 800 },
		{ stats: { [Stat.StatAttackPower]: 200, [Stat.StatRangedAttackPower]: 200 } },
	],
	['expose weakness without agility adds nothing', { exposeWeaknessUptime: 0.9 }, {}],
	["hunter's mark", { huntersMark: TristateEffect.TristateEffectRegular }, { stats: { [Stat.StatRangedAttackPower]: 440 } }],
	[
		"improved hunter's mark",
		{ huntersMark: TristateEffect.TristateEffectImproved },
		{ stats: { [Stat.StatRangedAttackPower]: 440, [Stat.StatAttackPower]: 110 } },
	],
];

describe('characterSheetDebuffStats', () => {
	it.each(cases)('%s', (_name, debuffs, expected) => {
		const result = characterSheetDebuffStats(Debuffs.create(debuffs)).toProto();
		result.stats.forEach((value, stat) => expect([Stat[stat], value]).toEqual([Stat[stat], expected.stats?.[stat as Stat] ?? 0]));
		result.pseudoStats.forEach((value, pseudoStat) =>
			expect([PseudoStat[pseudoStat], value]).toEqual([PseudoStat[pseudoStat], expected.pseudoStats?.[pseudoStat as PseudoStat] ?? 0]),
		);
	});

	it("credits the agility it is given for a hunter's own Expose Weakness", () => {
		const result = characterSheetDebuffStats(Debuffs.create({ exposeWeaknessUptime: 1, exposeWeaknessHunterAgility: 800 }), 1000);
		expect(result.getStat(Stat.StatAttackPower)).toBe(250);
	});
});
