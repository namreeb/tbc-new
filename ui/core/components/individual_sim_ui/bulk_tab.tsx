import { Tab } from 'bootstrap';
import clsx from 'clsx';
import tippy from 'tippy.js';
import { ref } from 'tsx-vanilla';

import i18n from '../../../i18n/config';
import { translateWeaponType } from '../../../i18n/localization';
import { trackEvent } from '../../../tracking/utils';
import { ASYNC_PROGRESS_POLL_INTERVAL_MS } from '../../../worker/types';
import { REPO_RELEASES_URL } from '../../constants/other';
import { IndividualSimUI } from '../../individual_sim_ui';
import {
	BulkGemOption,
	BulkGemSettings,
	BulkMetaGemCondition,
	BulkPoolItem,
	BulkSettings,
	BulkSimPhase,
	BulkSimRequest,
	BulkSimResult,
	BulkStatCap,
	BulkStatCapType,
	BulkStatConstraint,
	DistributionMetrics,
	ErrorOutcomeType,
	ProgressMetrics,
} from '../../proto/api';
import { GemColor, ItemRandomSuffix, ItemSlot, ItemSpec, WeaponType } from '../../proto/common';
import { ItemEffectRandPropPoints, SimDatabase, SimEnchant, SimGem, SimItem } from '../../proto/db';
import { StatCapType, UIEnchant, UIGem, UIItem } from '../../proto/ui';
import { ActionId } from '../../proto_utils/action_id';
import { EquippedItem } from '../../proto_utils/equipped_item';
import { Gear } from '../../proto_utils/gear';
import { getEmptyGemSocketIconUrl, getMetaGemCondition } from '../../proto_utils/gems';
import { canEquipItem, getEligibleItemSlots, isSecondaryItemSlot } from '../../proto_utils/utils';
import { RequestTypes } from '../../sim_signal_manager';
import { TypedEvent } from '../../typed_event';
import { getEnumValues, isExternal } from '../../utils';
import { CopyButton } from '../copy_button';
import { Exporter } from '../exporter';
import { ItemData } from '../gear_picker/item_list';
import SelectorModal from '../gear_picker/selector_modal';
import { BooleanPicker } from '../pickers/boolean_picker';
import { EnumPicker } from '../pickers/enum_picker';
import { ProgressTrackerModal } from '../progress_tracker_modal';
import { SimTab } from '../sim_tab';
import Toast from '../toast';
import BulkItemPickerGroup from './bulk/bulk_item_picker_group';
import BulkItemSearch from './bulk/bulk_item_search';
import BulkSimResultRenderer from './bulk/bulk_sim_results_renderer';
import BulkStatConstraintsPicker from './bulk/bulk_stat_constraints';
import { BULK_PHASES, BulkSimTimings, BulkTimingReport, formatDurationMs } from './bulk/bulk_timings';
import GemSelectorModal from './bulk/gem_selector_modal';
import { BulkSimItemSlot, bulkSimItemSlotToItemSlotPairs, getBulkItemSlotFromSlot } from './bulk/utils';
import { BulkGearJsonImporter } from './importers';

const WEB_ITERATIONS_LIMIT = 100_000;
const LOCAL_ITERATIONS_LIMIT = 5_000_000;

const WEB_COMBINATIONS_LIMIT = 50_000;
const LOCAL_COMBINATIONS_LIMIT = 100_000;

export interface TopGearResult {
	gear: Gear;
	dpsMetrics: DistributionMetrics;
}

// The batch sim request as JSON: what the server receives, and what the CLI
// (`wowsimcli bulk --infile`) replays.
class BulkSimRequestExporter extends Exporter {
	protected readonly simUI: IndividualSimUI<any>;

	constructor(
		parent: HTMLElement,
		simUI: IndividualSimUI<any>,
		private readonly bulkTab: BulkTab,
	) {
		super(parent, { title: i18n.t('bulk_tab.export.title'), allowDownload: true });
		this.simUI = simUI;
	}

	getData(): string {
		return BulkSimRequest.toJsonString(this.bulkTab.buildBulkSimRequest(), { prettySpaces: 2 });
	}
}

export class BulkTab extends SimTab {
	readonly simUI: IndividualSimUI<any>;
	readonly playerCanDualWield: boolean;

	readonly itemsChangedEmitter = new TypedEvent<void>();
	readonly settingsChangedEmitter = new TypedEvent<void>();

	private readonly setupTabElem: HTMLElement;
	private readonly resultsTabElem: HTMLElement;
	private readonly combinationsElem: HTMLElement;
	private readonly bulkSimButton: HTMLButtonElement;
	private readonly settingsContainer: HTMLElement;

	private resultsTab: Tab;
	protected progressTrackerModal: ProgressTrackerModal;

	readonly selectorModal: SelectorModal;

	// The main array we will use to store items with indexes. Null values are the result of removed items to avoid having to shift pickers over and over.
	protected items: Array<ItemSpec | null> = new Array<ItemSpec | null>();
	protected pickerGroups: Map<BulkSimItemSlot, BulkItemPickerGroup> = new Map();

	protected simStart: number = 0;
	protected combinations = 0;
	protected iterations = 0;
	protected isRunning: boolean = false;
	protected isCancelling = false;

	frozenItems: Map<BulkSimItemSlot, EquippedItem | null> = new Map([
		[BulkSimItemSlot.ItemSlotFinger, null],
		[BulkSimItemSlot.ItemSlotTrinket, null],
	]);
	frozenWeaponSlot: ItemSlot.ItemSlotMainHand | ItemSlot.ItemSlotOffHand | undefined = undefined;
	weaponTypeFilters: Map<ItemSlot.ItemSlotMainHand | ItemSlot.ItemSlotOffHand, WeaponType[]> = new Map([
		[ItemSlot.ItemSlotMainHand, []],
		[ItemSlot.ItemSlotOffHand, []],
	]);
	// Stat constraints that a gear combination must satisfy to be simmed,
	// e.g. Fire Resistance > 175. Checked against the character's final stats
	// (the same totals the stats panel shows) in runBatchSim, after gems are
	// filled in and before the combination is simmed. See BulkStatConstraintsPicker.
	statConstraints: BulkStatConstraint[] = [];
	// How many combinations the last run skipped for failing a constraint.
	protected skippedByConstraints = 0;
	// Phase timing of the last completed run, shown in the results tab.
	protected lastTimings: BulkTimingReport | null = null;
	// Sequence number so a stale server combination count never overwrites a newer one.
	private countRequestSeq = 0;

	// Whether sims run in-browser via WASM (vs a native server). Gates the
	// web-vs-native limits and default iterations; the batch itself runs in
	// the sim either way. Seeded from the hostname guess, corrected once a
	// worker reports its type.
	private isWasmSim = isExternal();
	fallbackGems: SimGem[];
	gemIconElements: HTMLImageElement[];

	protected topGearResults: TopGearResult[] | null = null;
	protected originalGear: Gear | null = null;
	protected originalGearResults: TopGearResult | null = null;

	constructor(parentElem: HTMLElement, simUI: IndividualSimUI<any>) {
		super(parentElem, simUI, { identifier: 'bulk-tab', title: i18n.t('bulk_tab.title') });

		this.simUI = simUI;
		this.playerCanDualWield = this.simUI.player.getPlayerSpec().canDualWield;

		this.simUI.sim
			.isWasm()
			.then(isWasm => {
				this.isWasmSim = isWasm;
				this.refreshCombinationsCount();
			})
			.catch(() => {});

		const setupTabBtnRef = ref<HTMLButtonElement>();
		const setupTabRef = ref<HTMLDivElement>();
		const resultsTabBtnRef = ref<HTMLButtonElement>();
		const resultsTabRef = ref<HTMLDivElement>();
		const settingsContainerRef = ref<HTMLDivElement>();
		const combinationsElemRef = ref<HTMLHeadingElement>();
		const bulkSimBtnRef = ref<HTMLButtonElement>();

		this.contentContainer.appendChild(
			<>
				<div className="bulk-tab-left tab-panel-left">
					<div className="bulk-tab-tabs">
						<ul className="nav nav-tabs" attributes={{ role: 'tablist' }}>
							<li className="nav-item" attributes={{ role: 'presentation' }}>
								<button
									className="nav-link active"
									type="button"
									attributes={{
										role: 'tab',
										// @ts-expect-error
										'aria-controls': 'bulkSetupTab',
										'aria-selected': true,
									}}
									dataset={{
										bsToggle: 'tab',
										bsTarget: `#bulkSetupTab`,
									}}
									ref={setupTabBtnRef}>
									{i18n.t('bulk_tab.tabs.setup')}
								</button>
							</li>
							<li className="nav-item" attributes={{ role: 'presentation' }}>
								<button
									className="nav-link"
									type="button"
									attributes={{
										role: 'tab',
										// @ts-expect-error
										'aria-controls': 'bulkResultsTab',
										'aria-selected': false,
									}}
									dataset={{
										bsToggle: 'tab',
										bsTarget: `#bulkResultsTab`,
									}}
									ref={resultsTabBtnRef}>
									{i18n.t('bulk_tab.tabs.results')}
								</button>
							</li>
						</ul>
						<div className="tab-content">
							<div id="bulkSetupTab" className="tab-pane fade active show" ref={setupTabRef} />
							<div id="bulkResultsTab" className="tab-pane fade show" ref={resultsTabRef}>
								<div className="d-flex align-items-center justify-content-center p-gap">{i18n.t('bulk_tab.results.run_simulation')}</div>
							</div>
						</div>
					</div>
				</div>
				<div className="bulk-tab-right tab-panel-right">
					<div className="bulk-settings-outer-container">
						<div className="bulk-settings-container" ref={settingsContainerRef}>
							<div className="bulk-combinations-count h4" ref={combinationsElemRef} />
							<button className="btn btn-primary bulk-settings-btn" ref={bulkSimBtnRef}>
								{i18n.t('bulk_tab.actions.simulate_batch')}
							</button>
						</div>
					</div>
				</div>
			</>,
		);

		this.setupTabElem = setupTabRef.value!;
		this.resultsTabElem = resultsTabRef.value!;

		this.combinationsElem = combinationsElemRef.value!;
		this.bulkSimButton = bulkSimBtnRef.value!;
		this.settingsContainer = settingsContainerRef.value!;

		new Tab(setupTabBtnRef.value!);
		this.resultsTab = new Tab(resultsTabBtnRef.value!);

		this.selectorModal = new SelectorModal(this.simUI.rootElem, this.simUI, this.simUI.player, undefined, {
			id: 'bulk-selector-modal',
		});

		this.progressTrackerModal = new ProgressTrackerModal(simUI.rootElem, {
			id: 'bulk-sim-progress-tracker',
			title: 'Bulk Sim',
			hasProgressBar: true,
			onCancel: () => {
				this.abortBulkSim();
			},
		});

		this.fallbackGems = Array.from({ length: 5 }, () => UIGem.create());
		this.gemIconElements = [];

		this.buildTabContent();

		this.simUI.sim.waitForInit().then(() => {
			this.loadSettings();
			const loadEquippedItems = () => {
				if (this.isRunning) {
					return;
				}

				// Clear all previously equipped items from the pickers
				for (const group of this.pickerGroups.values()) {
					if (group.has(-1)) {
						group.remove(-1, true);
					}
					if (group.has(-2)) {
						group.remove(-2, true);
					}
				}

				this.simUI.player.getEquippedItems().forEach((equippedItem, slot) => {
					const bulkSlot = getBulkItemSlotFromSlot(slot, this.playerCanDualWield);
					const group = this.pickerGroups.get(bulkSlot)!;
					const idx = this.isSecondaryItemSlot(slot) ? -2 : -1;
					if (equippedItem) {
						group.add(idx, equippedItem, true);
					}
				});

				this.itemsChangedEmitter.emit(TypedEvent.nextEventID());
			};
			const updateCombinationsCount = () => this.refreshCombinationsCount();

			this.simUI.player.gearChangeEmitter.on(() => loadEquippedItems());

			TypedEvent.onAny([this.settingsChangedEmitter, this.itemsChangedEmitter]).on(() => this.storeSettings());
			TypedEvent.onAny([this.itemsChangedEmitter, this.settingsChangedEmitter, this.simUI.sim.iterationsChangeEmitter]).on(() =>
				updateCombinationsCount(),
			);

			loadEquippedItems();
			updateCombinationsCount();
		});
	}

	private getSettingsKey(): string {
		return this.simUI.getStorageKey('bulk-settings.v1');
	}

	// Key used by the prototype before constraints joined BulkSettings.
	private getLegacyStatConstraintsKey(): string {
		return this.simUI.getStorageKey('bulk-stat-constraints.v1');
	}

	private loadSettings() {
		const storedSettings = window.localStorage.getItem(this.getSettingsKey());
		if (storedSettings != null) {
			let settings: BulkSettings;
			try {
				settings = BulkSettings.fromJsonString(storedSettings, {
					ignoreUnknownFields: true,
				});
			} catch {
				settings = BulkSettings.create();
			}

			this.addItems(settings.items, true);
			this.setFrozenItem(BulkSimItemSlot.ItemSlotFinger, this.getEquippedItemForFrozenSlot(BulkSimItemSlot.ItemSlotFinger, settings.freezeRingSlot));
			this.setFrozenItem(BulkSimItemSlot.ItemSlotTrinket, this.getEquippedItemForFrozenSlot(BulkSimItemSlot.ItemSlotTrinket, settings.freezeTrinketSlot));
			this.setFrozenWeaponSlot(settings.freezeWeaponSlot);
			this.setWeaponTypeFilter(ItemSlot.ItemSlotMainHand, settings.freezeMainhandWeaponSlots);
			this.setWeaponTypeFilter(ItemSlot.ItemSlotOffHand, settings.freezeOffhandWeaponSlots);
			this.setStatConstraints(settings.statConstraints.length ? settings.statConstraints : this.loadLegacyStatConstraints());
			this.fallbackGems = new Array<SimGem>(
				SimGem.create({ id: settings.defaultRedGem }),
				SimGem.create({ id: settings.defaultYellowGem }),
				SimGem.create({ id: settings.defaultBlueGem }),
				SimGem.create({ id: settings.defaultMetaGem }),
				SimGem.create({ id: settings.defaultPrismaticGem }),
			);

			this.fallbackGems.forEach((gem, idx) => {
				ActionId.fromItemId(gem.id)
					.fill()
					.then(filledId => {
						if (gem.id) {
							this.gemIconElements[idx].src = filledId.iconUrl;
							this.gemIconElements[idx].classList.remove('hide');
						}
					});
			});
		}
	}

	private storeSettings() {
		const settings = this.createBulkSettings();
		const setStr = BulkSettings.toJsonString(settings, { enumAsInteger: true });
		try {
			window.localStorage.setItem(this.getSettingsKey(), setStr);
			window.localStorage.removeItem(this.getLegacyStatConstraintsKey());
		} catch (e) {
			if (e && e instanceof DOMException && e.name === 'QuotaExceededError') {
				window.localStorage.removeItem(this.getSettingsKey());
			}
		}
	}

	// One-time migration from the prototype's separate storage key. The key is
	// removed the next time settings are stored.
	private loadLegacyStatConstraints(): BulkStatConstraint[] {
		const stored = window.localStorage.getItem(this.getLegacyStatConstraintsKey());
		if (stored == null) return [];

		try {
			const parsed: unknown = JSON.parse(stored);
			if (!Array.isArray(parsed)) return [];
			return parsed
				.filter(c => !!c && typeof c === 'object' && typeof c.stat === 'number' && typeof c.op === 'number' && typeof c.value === 'number')
				.map(c => BulkStatConstraint.create({ unitStat: { oneofKind: 'stat', stat: c.stat }, op: c.op, value: c.value }));
		} catch {
			return [];
		}
	}

	setStatConstraints(constraints: BulkStatConstraint[], eventID = TypedEvent.nextEventID()) {
		this.statConstraints = constraints.map(c => BulkStatConstraint.clone(c));
		this.settingsChangedEmitter.emit(eventID);
	}

	protected createBulkSettings(): BulkSettings {
		return BulkSettings.create({
			items: this.getItems(),
			defaultRedGem: this.fallbackGems[0].id,
			defaultYellowGem: this.fallbackGems[1].id,
			defaultBlueGem: this.fallbackGems[2].id,
			defaultMetaGem: this.fallbackGems[3].id,
			defaultPrismaticGem: this.fallbackGems[4].id,
			// Each combination is simmed with the sim's iteration setting, which
			// is also what the sidebar's iteration total is based on.
			iterationsPerCombo: this.simUI.sim.getIterations(),
			freezeRingSlot: this.getFrozenItemSlot(BulkSimItemSlot.ItemSlotFinger),
			freezeTrinketSlot: this.getFrozenItemSlot(BulkSimItemSlot.ItemSlotTrinket),
			freezeWeaponSlot: this.frozenWeaponSlot,
			freezeMainhandWeaponSlots: this.weaponTypeFilters.get(ItemSlot.ItemSlotMainHand)?.slice(),
			freezeOffhandWeaponSlots: this.weaponTypeFilters.get(ItemSlot.ItemSlotOffHand)?.slice(),
			statConstraints: this.statConstraints.map(c => BulkStatConstraint.clone(c)),
		});
	}

	protected createBulkItemsDatabase(): SimDatabase {
		const itemsDb = SimDatabase.create();
		for (const is of this.items.values()) {
			if (!is) continue;

			const item = this.simUI.sim.db.lookupItemSpec(is);
			if (!item) {
				throw new Error(`item with ID ${is.id} not found in database`);
			}
			itemsDb.items.push(SimItem.fromJson(UIItem.toJson(item.item), { ignoreUnknownFields: true }));

			const ieRpp = this.simUI.sim.db.getItemEffectRandPropPoints(item.ilvl);
			if (ieRpp) {
				itemsDb.itemEffectRandPropPoints.push(ItemEffectRandPropPoints.create(this.simUI.sim.db.getItemEffectRandPropPoints(item.ilvl)));
			}

			if (item.enchant) {
				itemsDb.enchants.push(
					SimEnchant.fromJson(UIEnchant.toJson(item.enchant), {
						ignoreUnknownFields: true,
					}),
				);
			}
			if (item.randomSuffix) {
				itemsDb.randomSuffixes.push(
					ItemRandomSuffix.fromJson(ItemRandomSuffix.toJson(item.randomSuffix), {
						ignoreUnknownFields: true,
					}),
				);
			}
			for (const gem of item.gems) {
				if (gem) {
					itemsDb.gems.push(SimGem.fromJson(UIGem.toJson(gem), { ignoreUnknownFields: true }));
				}
			}
		}
		for (const gem of this.fallbackGems) {
			if (gem.id > 0) {
				itemsDb.gems.push(gem);
			}
		}
		return itemsDb;
	}

	// Add an item to its eligible bulk sim item slot(s). Mainly used for importing and search
	addItem(item: ItemSpec) {
		this.addItems([item]);
	}
	// Add items to their eligible bulk sim item slot(s). Mainly used for importing and search
	addItems(items: ItemSpec[], silent = false) {
		items.forEach(item => {
			const equippedItem = this.simUI.sim.db.lookupItemSpec(item)?.withDynamicStats();
			if (equippedItem) {
				this.eligibleBulkSlots(equippedItem).forEach(bulkSlot => {
					const group = this.pickerGroups.get(bulkSlot)!;
					const idx = this.items.push(item) - 1;
					if (!group.add(idx, equippedItem, silent)) {
						this.items.pop();
					}
				});
			}
		});

		this.itemsChangedEmitter.emit(TypedEvent.nextEventID());
	}
	// Add an item to a particular bulk sim item slot
	addItemToSlot(item: ItemSpec, bulkSlot: BulkSimItemSlot) {
		const equippedItem = this.simUI.sim.db.lookupItemSpec(item)?.withDynamicStats();
		if (equippedItem) {
			const eligibleItemSlots = getEligibleItemSlots(equippedItem.item);
			if (!canEquipItem(equippedItem.item, this.simUI.player.getPlayerSpec(), eligibleItemSlots[0])) return;

			const idx = this.items.push(item) - 1;
			const group = this.pickerGroups.get(bulkSlot)!;
			if (!group.add(idx, equippedItem)) {
				this.items.pop();
			}
			this.itemsChangedEmitter.emit(TypedEvent.nextEventID());
		}
	}

	updateItem(idx: number, newItem: ItemSpec) {
		const equippedItem = this.simUI.sim.db.lookupItemSpec(newItem)?.withDynamicStats();
		if (equippedItem) {
			this.items[idx] = newItem;

			this.eligibleBulkSlots(equippedItem).forEach(bulkSlot => {
				const group = this.pickerGroups.get(bulkSlot)!;
				group.update(idx, equippedItem);
			});
		}

		this.itemsChangedEmitter.emit(TypedEvent.nextEventID());
	}

	removeItem(item: ItemSpec) {
		for (let idx = 0; idx < this.items.length; idx++) {
			if (this.items[idx] && ItemSpec.equals(this.items[idx]!, item)) {
				this.removeItemByIndex(idx);
				return;
			}
		}
	}
	removeItemByIndex(idx: number, silent = false) {
		if (idx < 0 || this.items.length < idx || !this.items[idx]) {
			new Toast({
				variant: 'error',
				body: i18n.t('bulk_tab.notifications.failed_to_remove_item'),
			});
			return;
		}

		const item = this.items[idx]!;
		const equippedItem = this.simUI.sim.db.lookupItemSpec(item);
		if (equippedItem) {
			this.items[idx] = null;

			// Try to find the matching item within its eligible groups
			getEligibleItemSlots(equippedItem.item).forEach(slot => {
				if (!canEquipItem(equippedItem.item, this.simUI.player.getPlayerSpec(), slot)) return;
				const bulkSlot = getBulkItemSlotFromSlot(slot, this.playerCanDualWield);
				const group = this.pickerGroups.get(bulkSlot)!;

				if (group.has(idx)) {
					group.remove(idx, silent);
				}
			});
			this.itemsChangedEmitter.emit(TypedEvent.nextEventID());
		}
	}

	clearItems() {
		for (let idx = 0; idx < this.items.length; idx++) {
			this.removeItemByIndex(idx, true);
		}
		this.items = new Array<ItemSpec>();
		this.itemsChangedEmitter.emit(TypedEvent.nextEventID());
	}

	hasItem(item: ItemSpec) {
		return this.items.some(i => !!i && ItemSpec.equals(i, item));
	}

	getItems(): Array<ItemSpec> {
		const result = new Array<ItemSpec>();
		this.items.forEach(spec => {
			if (!spec) return;

			result.push(ItemSpec.clone(spec));
		});
		return result;
	}

	protected buildTabContent() {
		this.buildSetupTabContent();
		this.buildResultsTabContent();
		this.buildBatchSettings();
	}

	private buildSetupTabContent() {
		const bagImportBtnRef = ref<HTMLButtonElement>();
		const favsImportBtnRef = ref<HTMLButtonElement>();
		const clearBtnRef = ref<HTMLButtonElement>();
		const exportBtnRef = ref<HTMLButtonElement>();
		this.setupTabElem.appendChild(
			<>
				{/* // TODO: Remove once we're more comfortable with the state of Batch sim */}
				<p className="mb-0" innerHTML={i18n.t('bulk_tab.description')} />
				{isExternal() && (
					<p className="mb-0">
						<a href={REPO_RELEASES_URL} target="_blank">
							<i className="fas fa-gauge-high me-1" />
							{i18n.t('bulk_tab.download_local')}
						</a>
					</p>
				)}
				<div className="bulk-gear-actions">
					<button className="btn btn-secondary" ref={bagImportBtnRef}>
						<i className="fa fa-download me-1" /> {i18n.t('bulk_tab.actions.import_bags')}
					</button>
					<button className="btn btn-secondary" ref={favsImportBtnRef}>
						<i className="fa fa-download me-1" /> {i18n.t('bulk_tab.actions.import_favorites')}
					</button>
					<button className="btn btn-secondary" ref={exportBtnRef}>
						<i className="fa fa-upload me-1" /> {i18n.t('bulk_tab.actions.export_json')}
					</button>
					<button className="btn btn-danger ms-auto" ref={clearBtnRef}>
						<i className="fas fa-times me-1" />
						{i18n.t('bulk_tab.actions.clear_items')}
					</button>
				</div>
			</>,
		);

		const bagImportButton = bagImportBtnRef.value!;
		const favsImportButton = favsImportBtnRef.value!;
		const clearButton = clearBtnRef.value!;

		bagImportButton.addEventListener('click', () => new BulkGearJsonImporter(this.simUI.rootElem, this.simUI, this).open());

		favsImportButton.addEventListener('click', () => {
			const filters = this.simUI.player.sim.getFilters();
			const items = filters.favoriteItems.map(itemID => ItemSpec.create({ id: itemID }));
			this.addItems(items);
		});

		clearButton.addEventListener('click', () => this.clearItems());
		exportBtnRef.value!.addEventListener('click', () => new BulkSimRequestExporter(this.simUI.rootElem, this.simUI, this).open());

		new BulkItemSearch(this.setupTabElem, this.simUI, this);

		const itemList = (<div className="bulk-gear-combo" />) as HTMLElement;
		this.setupTabElem.appendChild(itemList);

		getEnumValues<BulkSimItemSlot>(BulkSimItemSlot).forEach(bulkSlot => {
			if (this.playerCanDualWield && [BulkSimItemSlot.ItemSlotMainHand, BulkSimItemSlot.ItemSlotOffHand].includes(bulkSlot)) return;
			if (!this.playerCanDualWield && bulkSlot === BulkSimItemSlot.ItemSlotHandWeapon) return;
			this.pickerGroups.set(bulkSlot, new BulkItemPickerGroup(itemList, this.simUI, this, bulkSlot));
		});
	}

	private resetResultsTabContent() {
		this.resultsTabElem.replaceChildren();
	}

	private buildResultsTabContent() {
		if (!this.topGearResults || !this.originalGearResults) {
			return;
		}

		if (this.skippedByConstraints > 0) {
			this.resultsTabElem.appendChild(
				<div className="bulk-results-constraints-note">
					<i className="fas fa-filter me-1" />
					{i18n.t('bulk_tab.results.skipped_by_constraints', { skipped: this.skippedByConstraints, total: this.combinations })}
				</div>,
			);
		}

		for (const topGearResult of this.topGearResults) {
			new BulkSimResultRenderer(this.resultsTabElem, this.simUI, topGearResult, this.originalGearResults);
		}

		if (this.lastTimings) {
			this.buildTimingsBlock(this.lastTimings);
		}

		this.resultsTab.show();
	}

	// Phase timing of the last run, for comparing batch implementations.
	private buildTimingsBlock(report: BulkTimingReport) {
		const t = (key: string, options?: Record<string, unknown>) => i18n.t(`bulk_tab.results.timings.${key}`, options);
		const perItem = (ms: number, count: number) => (count > 0 ? formatDurationMs(ms / count) : '');
		const avg = (ms: number, count: number) => formatDurationMs(count > 0 ? ms / count : 0);
		const copyRef = ref<HTMLDivElement>();

		this.resultsTabElem.appendChild(
			<div className="bulk-results-timings">
				<div className="bulk-results-timings__header">
					<h6 className="mb-0">{t('title')}</h6>
					<div ref={copyRef} />
				</div>
				<table className="bulk-results-timings__table">
					<thead>
						<tr>
							<th>{t('phase')}</th>
							<th>{t('duration')}</th>
							<th>{t('count')}</th>
							<th>{t('per_item')}</th>
						</tr>
					</thead>
					<tbody>
						{BULK_PHASES.map(phase => (
							<tr>
								<td>{t(`phases.${phase}`)}</td>
								<td>{formatDurationMs(report.phases[phase].ms)}</td>
								<td>{report.phases[phase].count || ''}</td>
								<td>{perItem(report.phases[phase].ms, report.phases[phase].count)}</td>
							</tr>
						))}
						<tr className="bulk-results-timings__total">
							<td>{t('phases.total')}</td>
							<td>{formatDurationMs(report.totalMs)}</td>
							<td>{report.combinations}</td>
							<td>{perItem(report.totalMs, report.combinations)}</td>
						</tr>
					</tbody>
				</table>
				<div className="fs-content">
					{t('sims', {
						count: report.sims.count,
						iterations: report.iterationsPerSim,
						wall: formatDurationMs(report.sims.wallMs),
						avg: avg(report.sims.wallMs, report.sims.count),
					})}
				</div>
				<div className="fs-content">
					{t('poll_sleep', {
						sleep: formatDurationMs(report.sims.pollSleepMs),
						polls: report.sims.pollsBeforeFinal,
						interval: report.pollIntervalMs,
					})}
				</div>
				{report.statComputations.count > 0 && (
					<div className="fs-content">
						{t('stat_computations', {
							count: report.statComputations.count,
							wall: formatDurationMs(report.statComputations.wallMs),
							avg: avg(report.statComputations.wallMs, report.statComputations.count),
						})}
					</div>
				)}
			</div>,
		);

		new CopyButton(copyRef.value!, {
			extraCssClasses: ['btn-sm', 'btn-outline-primary'],
			text: t('copy_json'),
			getContent: () => JSON.stringify(report, null, 2),
		});
	}

	// Return whether or not the slot is considered secondary and the item should be grouped
	// This includes items in the Finger2 or Trinket2 slots, or OffHand for dual-wield specs
	// The bulk slots an item can be batched into, one entry each. Finger1/Finger2 - and both hands
	// for a dual-wielder - share a bulk slot, so dedupe on the bulk slot instead of skipping the
	// secondary physical slot: an off-hand-only item has no other eligible slot, and skipping it
	// dropped shields and off-hand weapons from the batch entirely.
	private eligibleBulkSlots(equippedItem: EquippedItem): BulkSimItemSlot[] {
		const bulkSlots: BulkSimItemSlot[] = [];
		getEligibleItemSlots(equippedItem.item).forEach(slot => {
			if (!canEquipItem(equippedItem.item, this.simUI.player.getPlayerSpec(), slot)) return;

			const bulkSlot = getBulkItemSlotFromSlot(slot, this.playerCanDualWield);
			if (!bulkSlots.includes(bulkSlot)) bulkSlots.push(bulkSlot);
		});
		return bulkSlots;
	}

	private isSecondaryItemSlot(slot: ItemSlot) {
		return isSecondaryItemSlot(slot) || (this.playerCanDualWield && slot === ItemSlot.ItemSlotOffHand);
	}

	private createFreezeWeaponTypePickers(container: HTMLElement, slot: ItemSlot.ItemSlotMainHand | ItemSlot.ItemSlotOffHand) {
		const weaponTypes = Array.from(
			new Set(
				this.simUI.player
					.getPlayerClass()
					.weaponTypes.filter(
						eligibleWeaponType =>
							slot === ItemSlot.ItemSlotMainHand ||
							(this.playerCanDualWield && ![WeaponType.WeaponTypePolearm, WeaponType.WeaponTypeStaff].includes(eligibleWeaponType.weaponType)),
					)
					.map(eligibleWeaponType => eligibleWeaponType.weaponType),
			),
		);

		if (!weaponTypes.length) return;

		const freezeWeaponTypeContainerRef = ref<HTMLDivElement>();
		const freezeWeaponTypeListRef = ref<HTMLDivElement>();

		container.appendChild(
			<div className={clsx('bulk-gear-freeze-weapontypes', this.frozenWeaponSlot === slot && 'hide')} ref={freezeWeaponTypeContainerRef}>
				<h6 className="mb-2">
					{slot === ItemSlot.ItemSlotMainHand
						? i18n.t('bulk_tab.settings.freeze_weapon_types.mainhand_label')
						: i18n.t('bulk_tab.settings.freeze_weapon_types.offhand_label')}
				</h6>
				<div className="fs-content mb-2">{i18n.t('bulk_tab.settings.freeze_weapon_types.tooltip')}</div>
				<div className="bulk-gear-freeze-weapontypes__list gap-1" ref={freezeWeaponTypeListRef}></div>
			</div>,
		);

		const updateVisibility = () => freezeWeaponTypeContainerRef.value?.parentElement?.classList.toggle('hide', this.frozenWeaponSlot === slot);
		const visibilityChange = this.settingsChangedEmitter.on(updateVisibility);
		this.addOnDisposeCallback(() => visibilityChange.dispose());

		weaponTypes.forEach(weaponType => {
			new BooleanPicker<BulkTab>(freezeWeaponTypeListRef.value!, this, {
				id: `bulk-${slot}-weapon-type-${weaponType}`,
				label: translateWeaponType(weaponType),
				inline: true,
				changedEvent: _modObj => this.settingsChangedEmitter,
				getValue: _modObj => this.weaponTypeFilters.get(slot)!.includes(weaponType),
				setValue: (eventID, _modObj, newValue: boolean) => {
					const filter = this.weaponTypeFilters.get(slot)!;
					this.setWeaponTypeFilter(slot, newValue ? [...filter, weaponType] : filter.filter(type => type !== weaponType), eventID);
				},
			});
		});
	}

	private setFrozenItem(
		bulkSlot: BulkSimItemSlot.ItemSlotFinger | BulkSimItemSlot.ItemSlotTrinket,
		item: EquippedItem | null,
		eventID = TypedEvent.nextEventID(),
	) {
		if (item === this.frozenItems.get(bulkSlot)) {
			return;
		}

		this.frozenItems.set(bulkSlot, item);
		this.settingsChangedEmitter.emit(eventID);
	}

	private getEquippedItemForFrozenSlot(bulkSlot: BulkSimItemSlot.ItemSlotFinger | BulkSimItemSlot.ItemSlotTrinket, itemSlot: number): EquippedItem | null {
		const slots = bulkSimItemSlotToItemSlotPairs.get(bulkSlot);
		if (!slots?.includes(itemSlot)) {
			return null;
		}

		return this.simUI.player.getGear().getEquippedItem(itemSlot) ?? null;
	}

	private getFrozenItemSlot(bulkSlot: BulkSimItemSlot.ItemSlotFinger | BulkSimItemSlot.ItemSlotTrinket): ItemSlot | undefined {
		const frozenItem = this.frozenItems.get(bulkSlot);
		const slots = bulkSimItemSlotToItemSlotPairs.get(bulkSlot);
		if (!frozenItem || !slots) {
			return undefined;
		}

		const currentGear = this.simUI.player.getGear();
		return (
			slots.find(slot => currentGear.getEquippedItem(slot) === frozenItem) ??
			slots.find(slot => currentGear.getEquippedItem(slot)?.equals(frozenItem)) ??
			undefined
		);
	}

	private setWeaponTypeFilter(
		slot: ItemSlot.ItemSlotMainHand | ItemSlot.ItemSlotOffHand,
		newFilter: WeaponType[],
		eventID = TypedEvent.nextEventID(),
		shouldEmit = true,
	): boolean {
		const currentFilter = this.weaponTypeFilters.get(slot)!;
		const hasChanged = currentFilter.length !== newFilter.length || currentFilter.some((weaponType, idx) => weaponType !== newFilter[idx]);

		if (!hasChanged) {
			return false;
		}

		this.weaponTypeFilters.set(slot, newFilter);
		if (shouldEmit) {
			this.settingsChangedEmitter.emit(eventID);
		}
		return true;
	}

	private clearWeaponTypeFilter(slot: ItemSlot.ItemSlotMainHand | ItemSlot.ItemSlotOffHand): boolean {
		return this.setWeaponTypeFilter(slot, [], undefined, false);
	}

	private setFrozenWeaponSlot(itemSlot: number | null, eventID = TypedEvent.nextEventID()): boolean {
		const newSlot = [ItemSlot.ItemSlotMainHand, ItemSlot.ItemSlotOffHand].includes(itemSlot ?? -1)
			? (itemSlot as ItemSlot.ItemSlotMainHand | ItemSlot.ItemSlotOffHand)
			: undefined;
		const filtersChanged = newSlot !== undefined && this.clearWeaponTypeFilter(newSlot);

		if (newSlot === this.frozenWeaponSlot && !filtersChanged) {
			return false;
		}

		this.frozenWeaponSlot = newSlot;
		this.settingsChangedEmitter.emit(eventID);
		return true;
	}

	protected buildBatchSettings() {
		this.bulkSimButton.addEventListener('click', () => this.runBatchSim());

		const socketsContainerRef = ref<HTMLDivElement>();
		const frozenRingDiv = ref<HTMLDivElement>();
		const frozenTrinketDiv = ref<HTMLDivElement>();
		const frozenWeaponDiv = ref<HTMLDivElement>();
		const mainHandWeaponTypesDiv = ref<HTMLDivElement>();
		const offHandWeaponTypesDiv = ref<HTMLDivElement>();
		const statConstraintsDiv = ref<HTMLDivElement>();

		this.settingsContainer.appendChild(
			<>
				<div className="fallback-gem-container">
					<h6>{i18n.t('bulk_tab.settings.fallback_gems')}</h6>
					<div ref={socketsContainerRef} className="sockets-container"></div>
				</div>
				<div ref={frozenRingDiv}></div>
				<div ref={frozenTrinketDiv}></div>
				{this.playerCanDualWield && (
					<>
						<div ref={frozenWeaponDiv}></div>
						<div ref={mainHandWeaponTypesDiv}></div>
						<div ref={offHandWeaponTypesDiv}></div>
					</>
				)}
				<div ref={statConstraintsDiv}></div>
			</>,
		);

		if (statConstraintsDiv.value) this.addChild(new BulkStatConstraintsPicker(statConstraintsDiv.value, this.simUI, this));

		if (frozenRingDiv.value)
			new EnumPicker<BulkTab>(frozenRingDiv.value, this, {
				id: 'freeze-ring',
				label: i18n.t('bulk_tab.settings.freeze_ring.label'),
				labelTooltip: i18n.t('bulk_tab.settings.freeze_ring.tooltip'),
				values: [
					{ name: i18n.t('common.none'), value: -1 },
					{ name: i18n.t('slots.finger_1', { ns: 'character' }), value: ItemSlot.ItemSlotFinger1 },
					{ name: i18n.t('slots.finger_2', { ns: 'character' }), value: ItemSlot.ItemSlotFinger2 },
				],
				changedEvent: _modObj => TypedEvent.onAny([this.settingsChangedEmitter, this.itemsChangedEmitter]),
				getValue: _modObj => {
					const frozenRing = this.frozenItems.get(BulkSimItemSlot.ItemSlotFinger);

					if (!frozenRing) {
						return -1;
					}

					const currentGear: Gear = this.simUI.player.getGear();

					if (currentGear.getEquippedItem(ItemSlot.ItemSlotFinger1)?.equals(frozenRing)) {
						return ItemSlot.ItemSlotFinger1;
					} else if (currentGear.getEquippedItem(ItemSlot.ItemSlotFinger2)?.equals(frozenRing)) {
						return ItemSlot.ItemSlotFinger2;
					} else {
						this.setFrozenItem(BulkSimItemSlot.ItemSlotFinger, null);
						return -1;
					}
				},
				setValue: (eventID, _modObj, newValue) => {
					let newItem: EquippedItem | null = null;

					if (newValue != -1) {
						newItem = this.simUI.player.getGear().getEquippedItem(newValue);
					}

					this.setFrozenItem(BulkSimItemSlot.ItemSlotFinger, newItem, eventID);
				},
			});

		if (frozenTrinketDiv.value)
			new EnumPicker<BulkTab>(frozenTrinketDiv.value, this, {
				id: 'freeze-trinket',
				label: i18n.t('bulk_tab.settings.freeze_trinket.label'),
				labelTooltip: i18n.t('bulk_tab.settings.freeze_trinket.tooltip'),
				values: [
					{ name: i18n.t('common.none'), value: -1 },
					{ name: i18n.t('slots.trinket_1', { ns: 'character' }), value: ItemSlot.ItemSlotTrinket1 },
					{ name: i18n.t('slots.trinket_2', { ns: 'character' }), value: ItemSlot.ItemSlotTrinket2 },
				],
				changedEvent: _modObj => TypedEvent.onAny([this.settingsChangedEmitter, this.itemsChangedEmitter]),
				getValue: _modObj => {
					const frozenTrinket = this.frozenItems.get(BulkSimItemSlot.ItemSlotTrinket);

					if (!frozenTrinket) {
						return -1;
					}

					const currentGear: Gear = this.simUI.player.getGear();

					if (currentGear.getEquippedItem(ItemSlot.ItemSlotTrinket1)?.equals(frozenTrinket)) {
						return ItemSlot.ItemSlotTrinket1;
					} else if (currentGear.getEquippedItem(ItemSlot.ItemSlotTrinket2)?.equals(frozenTrinket)) {
						return ItemSlot.ItemSlotTrinket2;
					} else {
						this.setFrozenItem(BulkSimItemSlot.ItemSlotTrinket, null);
						return -1;
					}
				},
				setValue: (eventID, _modObj, newValue) => {
					let newItem: EquippedItem | null = null;

					if (newValue != -1) {
						newItem = this.simUI.player.getGear().getEquippedItem(newValue);
					}

					this.setFrozenItem(BulkSimItemSlot.ItemSlotTrinket, newItem, eventID);
				},
			});

		if (this.playerCanDualWield) {
			if (frozenWeaponDiv.value)
				new EnumPicker<BulkTab>(frozenWeaponDiv.value, this, {
					id: 'freeze-weapon',
					label: i18n.t('bulk_tab.settings.freeze_weapon.label'),
					labelTooltip: i18n.t('bulk_tab.settings.freeze_weapon.tooltip'),
					values: [
						{ name: i18n.t('common.none'), value: -1 },
						{ name: i18n.t('slots.main_hand', { ns: 'character' }), value: ItemSlot.ItemSlotMainHand },
						{ name: i18n.t('slots.off_hand', { ns: 'character' }), value: ItemSlot.ItemSlotOffHand },
					],
					changedEvent: _modObj => TypedEvent.onAny([this.settingsChangedEmitter, this.itemsChangedEmitter]),
					getValue: _modObj => {
						if (!this.frozenWeaponSlot) {
							return -1;
						}

						return this.frozenWeaponSlot;
					},
					setValue: (eventID, _modObj, newValue) => {
						this.setFrozenWeaponSlot(newValue === -1 ? null : newValue, eventID);
					},
				});

			if (mainHandWeaponTypesDiv.value) this.createFreezeWeaponTypePickers(mainHandWeaponTypesDiv.value, ItemSlot.ItemSlotMainHand);
			if (offHandWeaponTypesDiv.value) this.createFreezeWeaponTypePickers(offHandWeaponTypesDiv.value, ItemSlot.ItemSlotOffHand);
		}

		Array<GemColor>(GemColor.GemColorRed, GemColor.GemColorYellow, GemColor.GemColorBlue, GemColor.GemColorMeta, GemColor.GemColorPrismatic).forEach(
			(socketColor, socketIndex) => {
				const gemContainerRef = ref<HTMLDivElement>();
				const gemIconRef = ref<HTMLImageElement>();
				const socketIconRef = ref<HTMLImageElement>();

				socketsContainerRef.value!.appendChild(
					<div ref={gemContainerRef} className="gem-socket-container">
						<img ref={gemIconRef} className="gem-icon hide" />
						<img ref={socketIconRef} className="socket-icon" />
					</div>,
				);

				this.gemIconElements.push(gemIconRef.value!);
				socketIconRef.value!.src = getEmptyGemSocketIconUrl(socketColor);

				let selector: GemSelectorModal;

				const onSelectHandler = (itemData: ItemData<UIGem>) => {
					this.fallbackGems[socketIndex] = itemData.item;
					this.storeSettings();
					ActionId.fromItemId(itemData.id)
						.fill()
						.then(filledId => {
							if (itemData.id) {
								this.gemIconElements[socketIndex].src = filledId.iconUrl;
								this.gemIconElements[socketIndex].classList.remove('hide');
							}
						});
					selector.close();
				};

				const onRemoveHandler = () => {
					this.fallbackGems[socketIndex] = UIGem.create();
					this.storeSettings();
					this.gemIconElements[socketIndex].classList.add('hide');
					this.gemIconElements[socketIndex].src = '';
					selector.close();
				};

				const openGemSelector = () => {
					if (!selector) selector = new GemSelectorModal(this.simUI.rootElem, this.simUI, socketColor, onSelectHandler, onRemoveHandler);
					selector.show();
				};

				this.gemIconElements[socketIndex].addEventListener('click', openGemSelector);
				gemContainerRef.value?.addEventListener('click', openGemSelector);
			},
		);
	}

	// The sidebar count: the same enumeration that will run the batch counts
	// and validates the request, in the sim (native server or wasm worker).
	private async refreshCombinationsCount() {
		const seq = ++this.countRequestSeq;
		try {
			const result = await this.simUI.sim.bulkSimCount(this.buildBulkSimRequest());
			if (seq != this.countRequestSeq) return;
			if (result.errorResult) {
				this.combinations = 0;
				this.iterations = 0;
				this.combinationsElem.replaceChildren(this.renderCombinationsCount(result.errorResult));
				return;
			}
			this.combinations = result.combinations;
			this.iterations = this.simUI.sim.getIterations() * this.combinations;
			this.combinationsElem.replaceChildren(this.renderCombinationsCount());
		} catch (error) {
			console.error(error);
			if (seq != this.countRequestSeq) return;
			this.combinations = 0;
			this.iterations = 0;
			this.combinationsElem.replaceChildren(this.renderCombinationsCount(String(error)));
		}
	}

	private renderCombinationsCount(error?: string): Element {
		this.bulkSimButton.disabled = !!error || !this.combinations || this.combinations > this.getCombinationsLimit();
		if (error) {
			return <span className="text-danger fs-content">{error}</span>;
		}

		const warningRef = ref<HTMLButtonElement>();
		const rtn = (
			<>
				<span className={clsx(this.showIterationsWarning() && 'text-danger')}>
					{this.combinations === 1
						? i18n.t('bulk_tab.settings.combination_singular')
						: i18n.t('bulk_tab.settings.combinations_count', { count: this.combinations })}
					<br />
					<small>
						{this.iterations} {i18n.t('bulk_tab.settings.iterations')}
					</small>
				</span>
				{this.showIterationsWarning() && (
					<button className="warning link-warning" ref={warningRef}>
						<i className="fas fa-exclamation-triangle fa-2x" />
					</button>
				)}
			</>
		);

		if (warningRef.value) {
			tippy(warningRef.value, {
				content: i18n.t('bulk_tab.warning.iterations_limit', { limit: this.getIterationsLimit() }),
				placement: 'left',
				popperOptions: {
					modifiers: [
						{
							name: 'flip',
							options: {
								fallbackPlacements: ['auto'],
							},
						},
					],
				},
			});
		}

		return rtn;
	}

	private showIterationsWarning(): boolean {
		return this.iterations > this.getIterationsLimit();
	}

	private getIterationsLimit(): number {
		return this.isWasmSim ? WEB_ITERATIONS_LIMIT : LOCAL_ITERATIONS_LIMIT;
	}

	private getCombinationsLimit(): number {
		return this.isWasmSim ? WEB_COMBINATIONS_LIMIT : LOCAL_COMBINATIONS_LIMIT;
	}

	private setReforgeProgress(currentRound: number, rounds: number) {
		this.progressTrackerModal.updateProgress({
			stage: 'reforging',
			title: i18n.t('bulk_tab.progress.reforging_rounds'),
			current: currentRound - 1,
			total: rounds,
			message: undefined,
		});
	}

	private setConstraintsProgress(checked: number, total: number) {
		this.progressTrackerModal.updateProgress({
			stage: 'constraints',
			title: i18n.t('bulk_tab.progress.checking_constraints'),
			current: checked,
			total,
			message: undefined,
		});
	}

	// The full batch request: the character as a single sim would send it, the
	// batch settings, the pool with slot eligibility, the gem optimizer's
	// inputs and the item data, so the server, the CLI and the JSON export all
	// work from the same message.
	buildBulkSimRequest(): BulkSimRequest {
		const player = this.simUI.player;
		const playerSpec = player.getPlayerSpec();
		const db = this.simUI.sim.db;
		const base = this.simUI.sim.makeRaidSimRequest(false);
		const settings = this.createBulkSettings();

		const pool: BulkPoolItem[] = [];
		const metaGemIds = new Set<number>();
		for (const spec of this.getItems()) {
			const equipped = db.lookupItemSpec(spec);
			if (!equipped) continue;
			const slots = getEligibleItemSlots(equipped.item).filter(slot => canEquipItem(equipped.item, playerSpec, slot));
			if (!slots.length) continue;
			pool.push(BulkPoolItem.create({ item: ItemSpec.clone(spec), slots }));
			equipped.curEquippedGems().forEach(gem => {
				if (gem.color == GemColor.GemColorMeta) metaGemIds.add(gem.id);
			});
		}
		player
			.getGear()
			.getAllGems()
			.forEach(gem => {
				if (gem.color == GemColor.GemColorMeta) metaGemIds.add(gem.id);
			});
		const fallbackMeta = db.lookupGem(this.fallbackGems[3].id);
		if (fallbackMeta) metaGemIds.add(fallbackMeta.id);

		const extraGems: UIGem[] = [];
		const gems = BulkGemSettings.create({ optimize: false });
		const reforger = this.simUI.reforger;
		if (reforger) {
			const gemSettings = reforger.getBatchGemSettings();
			gems.optimize = true;
			gems.statWeights = gemSettings.weights.toProto();
			gems.statCaps = gemSettings.statCaps.toProto();
			gems.undershootCaps = gemSettings.undershootCaps.toProto();
			gems.softCaps = gemSettings.softCaps.map(cap =>
				BulkStatCap.create({
					unitStat: cap.unitStat.isPseudoStat()
						? { oneofKind: 'pseudoStat', pseudoStat: cap.unitStat.getPseudoStat() }
						: { oneofKind: 'stat', stat: cap.unitStat.getStat() },
					breakpoints: cap.breakpoints.slice(),
					capType: cap.capType == StatCapType.TypeThreshold ? BulkStatCapType.BulkStatCapTypeThreshold : BulkStatCapType.BulkStatCapTypeSoftCap,
					postCapEps: cap.postCapEPs.slice(),
				}),
			);
			gems.eligibleGems = gemSettings.eligibleGems.map(({ gem, isJC }) => BulkGemOption.create({ gemId: gem.id, jewelcrafting: isJC }));
			gems.frozenSlots = gemSettings.frozenSlots;
			extraGems.push(...gemSettings.eligibleGems.map(({ gem }) => gem));
		}
		for (const id of metaGemIds) {
			const gem = db.lookupGem(id);
			if (gem) extraGems.push(gem);
			try {
				const condition = getMetaGemCondition(id);
				gems.metaGemConditions.push(
					BulkMetaGemCondition.create({
						gemId: id,
						minRed: condition.minRed,
						minYellow: condition.minYellow,
						minBlue: condition.minBlue,
						compareColorGreater: condition.compareColorGreater,
						compareColorLesser: condition.compareColorLesser,
					}),
				);
			} catch {
				// Unknown meta gem: the sim leaves it active.
			}
		}

		return BulkSimRequest.create({
			base,
			settings,
			gems,
			pool,
			canDualWield: this.playerCanDualWield,
			database: this.createBulkRequestDatabase(extraGems),
			topN: 5,
		});
	}

	// The pool's item data plus every gem the batch may socket.
	private createBulkRequestDatabase(extraGems: UIGem[]): SimDatabase {
		const database = this.createBulkItemsDatabase();
		const gemIds = new Set(database.gems.map(gem => gem.id));
		for (const gem of extraGems) {
			if (gemIds.has(gem.id)) continue;
			gemIds.add(gem.id);
			database.gems.push(SimGem.fromJson(UIGem.toJson(gem), { ignoreUnknownFields: true }));
		}
		return database;
	}

	private setBatchProgress(progress: ProgressMetrics) {
		const current = progress.completedSims;
		const total = progress.totalSims;
		switch (progress.bulkPhase) {
			case BulkSimPhase.BulkSimPhaseBuild:
				this.progressTrackerModal.updateProgress({
					stage: 'build',
					title: i18n.t('bulk_tab.progress.building_combinations'),
					current,
					total,
					message: undefined,
				});
				break;
			case BulkSimPhase.BulkSimPhaseGems:
				this.setReforgeProgress(current + 1, total);
				break;
			case BulkSimPhase.BulkSimPhaseConstraints:
				this.setConstraintsProgress(current, total);
				break;
			case BulkSimPhase.BulkSimPhaseSims: {
				const elapsedSeconds = (Date.now() - this.simStart) / 1000;
				const secondsRemaining = current > 0 ? (elapsedSeconds / current) * (total - current) : 0;
				this.progressTrackerModal.updateProgress({
					stage: 'sim',
					title: i18n.t('bulk_tab.progress.refining_rounds'),
					current,
					total,
					message: <div className="results-sim">{i18n.t('bulk_tab.progress.seconds_remaining', { seconds: Math.round(secondsRemaining) })}</div>,
				});
				break;
			}
		}
	}

	// The sim's per-phase timings in the shape the results tab renders.
	private timingReport(result: BulkSimResult, client: BulkTimingReport, progressPayloads: number): BulkTimingReport {
		const timings = result.timings!;
		const total = result.totalCombinations;
		const simmed = total - result.skippedByConstraints;
		const constrained = this.statConstraints.length > 0;
		return {
			totalMs: client.totalMs,
			combinations: total,
			iterationsPerSim: client.iterationsPerSim,
			phases: {
				build: { ms: timings.buildMs, count: total },
				gems: { ms: timings.gemsMs, count: total },
				constraints: { ms: timings.constraintsMs, count: constrained ? total : 0 },
				baselineSim: { ms: 0, count: 1 },
				candidateSims: { ms: timings.simsMs, count: simmed },
			},
			// Sims inside the batch are not polled individually: progress polls
			// only observe the run, so no wait is attributed to them.
			sims: { count: simmed + 1, wallMs: timings.simsMs, pollsBeforeFinal: Math.max(0, progressPayloads - 1), pollSleepMs: 0 },
			statComputations: { count: constrained ? total : 0, wallMs: timings.constraintsMs },
			pollIntervalMs: ASYNC_PROGRESS_POLL_INTERVAL_MS,
		};
	}

	// Runs the batch in the sim: one request (split across the wasm workers in
	// the browser), progress by phase, the ranked results back.
	private async runBatchSim() {
		if (this.isRunning) return;
		this.progressTrackerModal.show();

		trackEvent({
			action: 'sim',
			category: 'simulate',
			label: 'batch',
			value: this.combinations,
		});

		this.isRunning = true;
		this.isCancelling = false;
		this.bulkSimButton.disabled = true;
		this.topGearResults = null;
		this.originalGearResults = null;
		this.lastTimings = null;

		const timings = new BulkSimTimings(ASYNC_PROGRESS_POLL_INTERVAL_MS);
		timings.start();
		timings.iterationsPerSim = this.simUI.sim.getIterations();

		try {
			await this.simUI.sim.signalManager.abortType(RequestTypes.All);
			this.simStart = new Date().getTime();
			this.originalGear = this.simUI.player.getGear();
			this.resetResultsTabContent();

			const request = this.buildBulkSimRequest();
			timings.combinations = this.combinations;
			let progressPayloads = 0;
			const result = await this.simUI.sim.runBulkSim(request, progress => {
				progressPayloads += 1;
				this.setBatchProgress(progress);
			});

			if (result.error) {
				if (result.error.type == ErrorOutcomeType.ErrorOutcomeAborted) {
					this.isCancelling = true;
					return;
				}
				throw result.error.message;
			}

			this.combinations = result.totalCombinations;
			this.skippedByConstraints = result.skippedByConstraints;
			const topGearResults: TopGearResult[] = result.results.map(combo => ({
				gear: this.simUI.sim.db.lookupEquipmentSpec(combo.equipment!),
				dpsMetrics: combo.dps!,
			}));
			this.originalGearResults = { gear: this.originalGear, dpsMetrics: result.base!.dps! };
			this.topGearResults = [...topGearResults, this.originalGearResults].sort((a, b) => b.dpsMetrics.avg - a.dpsMetrics.avg);

			timings.finish();
			this.lastTimings = this.timingReport(result, timings.report(), progressPayloads);
			console.log('Batch sim timing', this.lastTimings);

			this.buildResultsTabContent();
		} catch (error) {
			console.error(error);
			if (!this.isCancelling && typeof error === 'string') {
				new Toast({
					variant: 'error',
					body: error,
				});
			}
		} finally {
			await this.simUI.player.setGearAsync(TypedEvent.nextEventID(), this.originalGear!);
			this.bulkSimButton.disabled = false;
			if (this.isCancelling) {
				new Toast({
					variant: 'error',
					body: i18n.t('bulk_tab.notifications.bulk_sim_cancelled'),
				});
			}
			this.isRunning = false;
			this.isCancelling = false;
			this.progressTrackerModal.hide();
		}
	}

	private async abortBulkSim() {
		if (this.isCancelling) return;

		try {
			this.isCancelling = true;
			await this.simUI.sim.signalManager.abortType(RequestTypes.All);
		} finally {
			this.bulkSimButton.disabled = false;
		}
	}
}
