import { SimRequest } from '../worker/types';
import {
	BulkSimPhase,
	BulkSimRequest,
	BulkSimRequestSplitRequest,
	BulkSimResult,
	BulkSimResultCombinationRequest,
	ErrorOutcome,
	ErrorOutcomeType,
	ProgressMetrics,
	RaidSimRequest,
	RaidSimRequestSplitRequest,
	RaidSimResult,
	RaidSimResultCombinationRequest,
	StatWeightsCalcRequest,
	StatWeightsRequest,
	StatWeightsResult,
	StatWeightsStatResultData,
} from './proto/api';
import { SimSignals } from './sim_signal_manager';
import { generateRequestId, WorkerPool, WorkerProgressCallback } from './worker_pool';

class ConcurrentSimProgress {
	readonly concurrency: number;
	readonly iterationsTotal: number;
	private readonly iterationsDone: number[];
	private readonly dpsValues: number[];
	private readonly hpsValues: number[];
	readonly finalResults: RaidSimResult[];

	constructor(concurrency: number, totalIterations: number) {
		this.concurrency = concurrency;
		this.iterationsTotal = totalIterations;
		this.iterationsDone = Array(this.concurrency).fill(0);
		this.dpsValues = Array(this.concurrency).fill(0);
		this.hpsValues = Array(this.concurrency).fill(0);
		this.finalResults = Array(this.concurrency);
	}

	getIterationsDone(): number {
		let total = 0;
		for (const done of this.iterationsDone) {
			total += done;
		}
		return total;
	}

	getDpsAvg(): number {
		let total = 0;
		for (const done of this.dpsValues) {
			total += done;
		}
		return total / this.concurrency;
	}

	getHpsAvg(): number {
		let total = 0;
		for (const done of this.hpsValues) {
			total += done;
		}
		return total / this.concurrency;
	}

	updateProgress(idx: number, msg: ProgressMetrics) {
		this.iterationsDone[idx] = msg.completedIterations;
		this.dpsValues[idx] = msg.dps;
		this.hpsValues[idx] = msg.hps;

		if (msg.finalRaidResult) {
			this.finalResults[idx] = msg.finalRaidResult;
		}
	}

	makeProgressMetrics(): ProgressMetrics {
		return ProgressMetrics.create({
			totalIterations: this.iterationsTotal,
			completedIterations: this.getIterationsDone(),
			dps: this.getDpsAvg(),
			hps: this.getHpsAvg(),
		});
	}
}

interface SimRunResult {
	errorResult?: RaidSimResult;
	results: RaidSimResult[];
	progressMetricsFinal: ProgressMetrics;
}

const runSims = (
	requests: RaidSimRequest[],
	totalIterations: number,
	wp: WorkerPool,
	onProgress: WorkerProgressCallback,
	signals: SimSignals,
): Promise<SimRunResult> => {
	return new Promise(resolve => {
		const csp = new ConcurrentSimProgress(requests.length, totalIterations);
		let progressCounter = 0;
		let running = requests.length;

		const progressHandler = (idx: number, pm: ProgressMetrics) => {
			if (!running) return;

			csp.updateProgress(idx, pm);

			progressCounter++;
			if (progressCounter % running == 0) {
				onProgress(csp.makeProgressMetrics());
			}

			if (pm.finalRaidResult) {
				running--;
				let errorResult: RaidSimResult | undefined;

				if (pm.finalRaidResult.error) {
					if (pm.finalRaidResult.error.type == ErrorOutcomeType.ErrorOutcomeError) {
						console.error(`Worker ${idx} had an error!`);
					}
					errorResult = pm.finalRaidResult;
					signals.abort.trigger();
				}

				if (errorResult || running == 0) {
					running = 0;
					const finalProgressMetrics = csp.makeProgressMetrics();
					finalProgressMetrics.finalRaidResult = errorResult;
					onProgress(finalProgressMetrics);
					resolve({
						errorResult: errorResult,
						results: csp.finalResults,
						progressMetricsFinal: finalProgressMetrics,
					});
					return;
				}
			}
		};

		for (let i = 0; i < requests.length; i++) {
			wp.raidSimAsync(requests[i], pm => progressHandler(i, pm), signals);
		}
	});
};

const makeAndSendRaidSimError = (err: string | ErrorOutcome, onProgress: WorkerProgressCallback): RaidSimResult => {
	const errRes = RaidSimResult.create();
	if (typeof err === 'string') {
		console.error(err);
		errRes.error = ErrorOutcome.create({ message: err });
	} else {
		if (err.message) console.error(err.message);
		errRes.error = err;
	}
	onProgress(ProgressMetrics.create({ finalRaidResult: errRes }));
	return errRes;
};

export const runConcurrentSim = async (
	request: RaidSimRequest,
	workerPool: WorkerPool,
	onProgress: WorkerProgressCallback,
	signals: SimSignals,
): Promise<RaidSimResult> => {
	console.log(`Sending requests split for ${workerPool.getNumWorkers()} splits.`);

	const splitResult = await workerPool.raidSimRequestSplit(
		RaidSimRequestSplitRequest.create({
			splitCount: workerPool.getNumWorkers(),
			request: request,
		}),
	);

	if (splitResult.errorResult) {
		return makeAndSendRaidSimError(splitResult.errorResult, onProgress);
	}

	if (signals.abort.isTriggered()) {
		return makeAndSendRaidSimError(ErrorOutcome.create({ type: ErrorOutcomeType.ErrorOutcomeAborted }), onProgress);
	}

	console.log(`Running ${request.simOptions!.iterations} iterations on ${splitResult.splitsDone} concurrent sims...`);

	const simRes = await runSims(splitResult.requests, request.simOptions!.iterations, workerPool, onProgress, signals);

	if (simRes.errorResult && simRes.errorResult.error) {
		return makeAndSendRaidSimError(simRes.errorResult.error, onProgress);
	}

	if (signals.abort.isTriggered()) {
		return makeAndSendRaidSimError(ErrorOutcome.create({ type: ErrorOutcomeType.ErrorOutcomeAborted }), onProgress);
	}

	console.log(`All ${splitResult.splitsDone} sims finished successfully. Combining ${simRes.results.length} results.`);

	const combiResult = await workerPool.raidSimResultCombination(
		RaidSimResultCombinationRequest.create({
			results: simRes.results,
		}),
	);

	if (combiResult.error) {
		return makeAndSendRaidSimError(combiResult.error, onProgress);
	}

	simRes.progressMetricsFinal.finalRaidResult = combiResult;
	onProgress(simRes.progressMetricsFinal);

	return combiResult;
};

const makeAndSendWeightsError = (err: string | ErrorOutcome, onProgress: WorkerProgressCallback): StatWeightsResult => {
	const errRes = RaidSimResult.create();
	if (typeof err === 'string') {
		console.error(err);
		errRes.error = ErrorOutcome.create({ message: err });
	} else {
		if (err.message) console.error(err.message);
		errRes.error = err;
	}
	onProgress(ProgressMetrics.create({ finalWeightResult: errRes }));
	return errRes;
};

export const runConcurrentStatWeights = async (
	request: StatWeightsRequest,
	workerPool: WorkerPool,
	onProgress: WorkerProgressCallback,
	signals: SimSignals,
): Promise<StatWeightsResult> => {
	console.log('Getting stat weight sim requests.');

	const id = generateRequestId(SimRequest.statWeightsAsync);

	const manualResponse = await workerPool.statWeightRequests(request);
	manualResponse.baseRequest!.requestId = id;

	if (signals.abort.isTriggered()) {
		return makeAndSendWeightsError(ErrorOutcome.create({ type: ErrorOutcomeType.ErrorOutcomeAborted }), onProgress);
	}

	let iterationsTotal = manualResponse.baseRequest!.simOptions!.iterations;
	let iterationsDone = 0;
	let simsTotal = 1;
	let simsDone = 0;

	for (const statReqData of manualResponse.statSimRequests) {
		if (statReqData.requestLow) {
			statReqData.requestLow!.requestId = id;
			iterationsTotal += statReqData.requestLow.simOptions!.iterations;
			simsTotal += 1;
		}
		statReqData.requestHigh!.requestId = id;
		iterationsTotal += statReqData.requestHigh!.simOptions!.iterations;
		simsTotal += 1;
	}

	console.log(`Need to run a total of ${simsTotal} sims and ${iterationsTotal} iterations.`);

	let lastIterations = 0;
	const progressHandler = (pm: ProgressMetrics) => {
		iterationsDone += pm.completedIterations - lastIterations;
		lastIterations = pm.completedIterations;

		onProgress(
			ProgressMetrics.create({
				totalIterations: iterationsTotal,
				completedIterations: iterationsDone,
				totalSims: simsTotal,
				completedSims: simsDone,
			}),
		);

		if (pm.finalRaidResult) simsDone++;
	};

	const baseLine = await runConcurrentSim(manualResponse.baseRequest!, workerPool, progressHandler, signals);
	if (baseLine.error) return makeAndSendWeightsError(baseLine.error, onProgress);

	const calcRequest = StatWeightsCalcRequest.create({
		baseResult: baseLine,
		epReferenceStat: manualResponse.epReferenceStat,
		statSimResults: [],
	});

	for (const statReqData of manualResponse.statSimRequests) {
		if (signals.abort.isTriggered()) return makeAndSendWeightsError(ErrorOutcome.create({ type: ErrorOutcomeType.ErrorOutcomeAborted }), onProgress);

		lastIterations = 0;
		let lowRes: RaidSimResult | undefined;
		if (statReqData.requestLow) {
			lowRes = await runConcurrentSim(statReqData.requestLow, workerPool, progressHandler, signals);
			if (lowRes.error) return makeAndSendWeightsError(lowRes.error, onProgress);
		}

		lastIterations = 0;
		const highRes = await runConcurrentSim(statReqData.requestHigh!, workerPool, progressHandler, signals);
		if (highRes.error) return makeAndSendWeightsError(highRes.error, onProgress);

		calcRequest.statSimResults.push(
			StatWeightsStatResultData.create({
				statData: statReqData.statData,
				resultLow: lowRes,
				resultHigh: highRes,
			}),
		);
	}

	console.log(`All ${simsTotal} sims finished successfully. Computing weights.`);

	const weightResult = await workerPool.statWeightCompute(calcRequest);
	if (weightResult.error) return makeAndSendWeightsError(weightResult.error, onProgress);
	onProgress(ProgressMetrics.create({ finalWeightResult: weightResult }));
	return weightResult;
};

// Progress of a batch split into pieces. The batch is as far along as its
// slowest piece, so that piece's phase is reported, with the work every piece
// has done in that phase summed. A piece reports a phase it has nothing to do
// in (no constraints, gems off) not at all, so only pieces that have reported
// the phase count.
class ConcurrentBulkProgress {
	private readonly phases: Map<BulkSimPhase, { completed: number; total: number }>[];
	private readonly latestPhase: BulkSimPhase[];
	readonly finalResults: BulkSimResult[];

	constructor(concurrency: number) {
		this.phases = Array.from({ length: concurrency }, () => new Map());
		this.latestPhase = Array(concurrency).fill(BulkSimPhase.BulkSimPhaseUnknown);
		this.finalResults = Array(concurrency);
	}

	updateProgress(idx: number, msg: ProgressMetrics) {
		if (msg.finalBulkResult) {
			this.finalResults[idx] = msg.finalBulkResult;
			return;
		}
		this.latestPhase[idx] = msg.bulkPhase;
		this.phases[idx].set(msg.bulkPhase, { completed: msg.completedSims, total: msg.totalSims });
	}

	makeProgressMetrics(): ProgressMetrics {
		const running = this.latestPhase.filter((_, idx) => !this.finalResults[idx]);
		const phase = running.length ? Math.min(...running) : BulkSimPhase.BulkSimPhaseSims;
		let completed = 0;
		let total = 0;
		for (const piece of this.phases) {
			const done = piece.get(phase);
			if (!done) continue;
			completed += done.completed;
			total += done.total;
		}
		return ProgressMetrics.create({ bulkPhase: phase, completedSims: completed, totalSims: total });
	}
}

const makeAndSendBulkSimError = (err: string | ErrorOutcome, onProgress: WorkerProgressCallback): BulkSimResult => {
	const errRes = BulkSimResult.create();
	if (typeof err === 'string') {
		console.error(err);
		errRes.error = ErrorOutcome.create({ message: err });
	} else {
		if (err.message) console.error(err.message);
		errRes.error = err;
	}
	onProgress(ProgressMetrics.create({ finalBulkResult: errRes }));
	return errRes;
};

// Runs a batch across the wasm worker pool: the batch is split by combination
// range into one piece per worker, each piece runs the whole pipeline on its
// range, and the pieces' results are merged. Splitting and merging are done by
// the sim itself, so the outcome is the one a single worker would produce.
export const runConcurrentBulkSim = async (
	request: BulkSimRequest,
	workerPool: WorkerPool,
	onProgress: WorkerProgressCallback,
	signals: SimSignals,
): Promise<BulkSimResult> => {
	const splitResult = await workerPool.bulkSimRequestSplit(
		BulkSimRequestSplitRequest.create({
			splitCount: workerPool.getNumWorkers(),
			request: request,
		}),
	);

	if (splitResult.errorResult) {
		return makeAndSendBulkSimError(splitResult.errorResult, onProgress);
	}

	if (signals.abort.isTriggered()) {
		return makeAndSendBulkSimError(ErrorOutcome.create({ type: ErrorOutcomeType.ErrorOutcomeAborted }), onProgress);
	}

	console.log(`Running batch as ${splitResult.splitsDone} concurrent pieces...`);

	const pieces = splitResult.requests;
	const progress = new ConcurrentBulkProgress(pieces.length);
	const errorResult = await new Promise<BulkSimResult | undefined>(resolve => {
		let running = pieces.length;

		const progressHandler = (idx: number, pm: ProgressMetrics) => {
			if (!running) return;

			progress.updateProgress(idx, pm);

			if (!pm.finalBulkResult) {
				onProgress(progress.makeProgressMetrics());
				return;
			}

			running--;
			let error: BulkSimResult | undefined;
			if (pm.finalBulkResult.error) {
				if (pm.finalBulkResult.error.type == ErrorOutcomeType.ErrorOutcomeError) {
					console.error(`Batch piece ${idx} had an error!`);
				}
				error = pm.finalBulkResult;
				signals.abort.trigger();
			}

			if (error || running == 0) {
				running = 0;
				resolve(error);
			}
		};

		for (let i = 0; i < pieces.length; i++) {
			workerPool.bulkSimAsync(pieces[i], pm => progressHandler(i, pm), signals);
		}
	});

	if (errorResult?.error) {
		return makeAndSendBulkSimError(errorResult.error, onProgress);
	}

	if (signals.abort.isTriggered()) {
		return makeAndSendBulkSimError(ErrorOutcome.create({ type: ErrorOutcomeType.ErrorOutcomeAborted }), onProgress);
	}

	const combined = await workerPool.bulkSimResultCombination(
		BulkSimResultCombinationRequest.create({
			results: progress.finalResults,
			topN: request.topN,
		}),
	);

	onProgress(ProgressMetrics.create({ finalBulkResult: combined }));
	return combined;
};
