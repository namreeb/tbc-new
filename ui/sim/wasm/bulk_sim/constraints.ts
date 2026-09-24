import { BulkSimRequest, BulkSimStage, ComputeStatsRequest, ErrorOutcome, ErrorOutcomeType, Raid } from '@generated/proto/api';
import { UnitStats } from '@generated/proto/common';
import { queue } from 'async';

import { finalStatsPassConstraints } from '../../bulk/stat_constraints';
import { Database } from '../../proto/database';
import { SimSignals } from '../../sim_signal_manager';
import { WorkerPool, WorkerProgressCallback } from '../../workers/worker_pool';
import { makeBulkSimStageProgressEmitter } from './progress';
import { ConcurrentBulkSimCandidate } from './types';

// Stat constraints (BulkSettings.stat_constraints) drop candidates whose final stats fail
// any constraint, after gem optimization and before the staged sims. Final stats are the
// ones the character sheet shows, so a surviving candidate displays matching numbers once
// equipped. Mirrors sim/core/bulk/constraints.go.

export type BulkSimConstraintFilterResult = {
	candidates: ConcurrentBulkSimCandidate[];
	skipped: number;
	error?: ErrorOutcome;
};

// The scratch request avoids cloning the raid and its merged SimDatabase per candidate.
// Reuse is safe because each call mutates it and the worker call encodes it to binary
// synchronously, before the first await.
const makeComputeStatsRequestForCandidate = (
	request: BulkSimRequest,
	candidate: ConcurrentBulkSimCandidate,
	scratch: ComputeStatsRequest,
): ComputeStatsRequest => {
	const player = scratch.raid!.parties[0].players[0];
	player.equipment = candidate.gear;
	// Keep weapon stone imbues in sync with this candidate's weapon types, as the sims do.
	if (player.consumables && candidate.gear) {
		player.consumables = Database.getSync().lookupEquipmentSpec(candidate.gear).adjustImbues(player.consumables);
	}
	return scratch;
};

// Keeps the candidates whose final stats satisfy every constraint, in order. With no
// constraints every candidate survives without any stats being computed. Progress is
// reported as the constraints stage; an abort or the first stats error ends the check.
export const filterBulkSimCandidatesByConstraints = async (
	request: BulkSimRequest,
	candidates: ConcurrentBulkSimCandidate[],
	workerPool: WorkerPool,
	onProgress: WorkerProgressCallback,
	signals: SimSignals,
): Promise<BulkSimConstraintFilterResult> => {
	const constraints = request.bulkSettings?.statConstraints ?? [];
	if (!constraints.length || !candidates.length) return { candidates, skipped: 0 };

	const emitter = makeBulkSimStageProgressEmitter(onProgress, BulkSimStage.BulkSimStageConstraints, candidates.length, 0);
	emitter.report(0, 0, 0);

	const scratch = ComputeStatsRequest.create({
		raid: Raid.clone(request.baseRequest!.raid!),
		encounter: request.baseRequest!.encounter,
	});
	const passes = new Array<boolean>(candidates.length).fill(false);
	let completed = 0;
	let error: ErrorOutcome | undefined;
	// Same worker-saturating queue the candidate batch and the gem pre-pass use.
	const statsQueue = queue<{ candidate: ConcurrentBulkSimCandidate; idx: number }, Error>(
		async ({ candidate, idx }) => {
			if (error || signals.abort.isTriggered()) return;
			const result = await workerPool.computeStats(makeComputeStatsRequestForCandidate(request, candidate, scratch));
			if (result.errorResult) {
				error ??= ErrorOutcome.create({ message: result.errorResult });
				return;
			}
			const finalStats = result.raidStats?.parties[0]?.players[0]?.finalStats ?? UnitStats.create();
			passes[idx] = finalStatsPassConstraints(constraints, finalStats);
			completed++;
			emitter.report(completed, 0, 0);
		},
		Math.max(1, Math.min(workerPool.getNumWorkers(), candidates.length)),
	);
	const queueErrorPromise = statsQueue.error();
	candidates.forEach((candidate, idx) => statsQueue.push({ candidate, idx }));
	await Promise.race([statsQueue.drain(), queueErrorPromise]);

	if (signals.abort.isTriggered()) return { candidates: [], skipped: 0, error: ErrorOutcome.create({ type: ErrorOutcomeType.ErrorOutcomeAborted }) };
	if (error) return { candidates: [], skipped: 0, error };
	const survivors = candidates.filter((_, idx) => passes[idx]);
	return { candidates: survivors, skipped: candidates.length - survivors.length };
};
