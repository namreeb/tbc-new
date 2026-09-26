// The browser's final-stats check judges constraints on the values the stats panel shows, which
// include the raid debuffs the panel attributes to the character (see debuff_stats.ts).
import { BulkSimRequest, BulkStatConstraint, BulkStatConstraintOp, ComputeStatsResult, Raid, RaidSimRequest } from '@generated/proto/api';
import { Debuffs, EquipmentSpec, PseudoStat, TristateEffect, UnitStats } from '@generated/proto/common';
import { describe, expect, it, vi } from 'vitest';

import type { SimSignals } from '../../sim_signal_manager';
import type { WorkerPool } from '../../workers/worker_pool';
import { filterBulkSimCandidatesByConstraints } from './constraints';

const RAW_SPELL_CRIT = 20;
const signals = { abort: { isTriggered: () => false } } as unknown as SimSignals;

const finalStats = () => {
	const stats = UnitStats.create({ stats: new Array(50).fill(0), pseudoStats: new Array(40).fill(0) });
	stats.pseudoStats[PseudoStat.PseudoStatSpellCritPercent] = RAW_SPELL_CRIT;
	return stats;
};
const workerPool = {
	getNumWorkers: () => 2,
	computeStats: vi.fn(async () => ComputeStatsResult.create({ raidStats: { parties: [{ players: [{ finalStats: finalStats() }] }] } })),
} as unknown as WorkerPool;

const check = (value: number) =>
	filterBulkSimCandidatesByConstraints(
		BulkSimRequest.create({
			baseRequest: RaidSimRequest.create({
				raid: Raid.create({
					parties: [{ players: [{}] }],
					debuffs: Debuffs.create({ improvedSealOfTheCrusader: TristateEffect.TristateEffectImproved }),
				}),
			}),
			bulkSettings: {
				statConstraints: [
					BulkStatConstraint.create({
						unitStat: { oneofKind: 'pseudoStat', pseudoStat: PseudoStat.PseudoStatSpellCritPercent },
						op: BulkStatConstraintOp.BulkStatConstraintOpGreaterThanOrEqual,
						value,
					}),
				],
			},
		}),
		[{ index: 0, gear: EquipmentSpec.create() }],
		workerPool,
		vi.fn(),
		signals,
	);

describe('filterBulkSimCandidatesByConstraints', () => {
	it('counts the crit Improved Seal of the Crusader adds, as the stats panel does', async () => {
		expect((await check(RAW_SPELL_CRIT + 3)).skipped).toBe(0);
	});

	it('still drops a candidate below what the panel shows', async () => {
		expect((await check(RAW_SPELL_CRIT + 3.5)).skipped).toBe(1);
	});
});
