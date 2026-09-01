'use client'

/* eslint-disable react/no-unescaped-entities, react/jsx-key --
 * Guide prose ported from the console (draft-formance-poc components/guide/v3):
 * quote characters render as-is, and GuideTable row-cell nodes render inside
 * keyed <td> elements (no missing-key at runtime). Both rules are noise here. */

// The reconciliation ("Ledger Clarity") User Guide.
//
// Ported verbatim from the console shell's reconcile guide sections
// (draft-formance-poc apps/web/components/guide/v3/content.tsx) when
// reconciliation was retrofitted as a standalone, self-serving module — only the
// presentation primitives were translated (Section/KeyValueTable/link/h3 →
// Section/GuideTable/AppLink/SubHeading). Rendered by ./guide-shell.

import * as React from 'react'
import { Scale, ListChecks, History, Bell, LineChart } from 'lucide-react'
import { Section, SubHeading, P, Callout, GuideTable, AppLink, Code } from './primitives'

export interface GuideSection {
	id: string
	group: string
	label: string
	icon: React.ReactNode
	render: () => React.ReactNode
}

export const DEFAULT_SECTION = 'overview'

export const GUIDE_SECTIONS: GuideSection[] = [
	{
		id: 'overview',
		group: 'Reconciliation',
		label: 'Reconcile',
		icon: <Scale className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="overview"
				eyebrow="Reconciliation"
				title="Reconcile"
				intro="Reconcile (“Ledger Clarity”) watches the balances and relationships that must always hold across your ledgers, records every check it runs, flags the ones that break, and helps you resolve or accept each break. It is a business process (observe, detect, resolve), not only a rules engine."
			>
				<SubHeading id="v3-rec-where">Where it lives</SubHeading>
				<P>
					Open <strong>Reconcile</strong>{' '}from the sidebar. Unlike the ledger explorer, it is not tied to
					the ledger you are currently exploring: a reconciliation rule names its own ledgers, so it can
					compare accounts <em>within</em> one ledger or <em>across</em> several. It talks to its own
					reconciliation service. A status pill in the top-right shows whether that service is reachable.
					Click it to point the UI at a different recon instance (for example, one wired to a staging
					ledger).
				</P>

				<SubHeading id="v3-rec-process">Observe → detect → resolve</SubHeading>
				<GuideTable
					caption="The reconciliation process"
					columns={['Step', 'What happens', 'Where']}
					rows={[
						[<strong key="observe">Observe</strong>, 'You define rules: the invariants that must hold on your ledgers', <span key="rules-link"><AppLink href="/guide?section=rules">Rules & templates</AppLink></span>],
						[<strong key="detect">Detect</strong>, 'Each evaluation records a PASS, FAIL, or ERROR receipt and, on a failure, may open an alert (a break)', <span key="captures-link"><AppLink href="/guide?section=activity">Rule activity</AppLink></span>],
						[<strong key="resolve">Resolve</strong>, 'A human works each break: acknowledge, resolve, accept, or snooze it', <span key="alerts-link"><AppLink href="/guide?section=alerts">Alerts & resolution</AppLink></span>],
					]}
				/>

				<SubHeading id="v3-rec-tabs">The four tabs</SubHeading>
				<GuideTable
					caption="Reconcile tabs"
					columns={['Tab', 'Purpose']}
					rows={[
						[<strong key="overview">Overview</strong>, 'Clickable shortcuts to open alerts, handled alerts, rules, and the most recent break activity'],
						[<strong key="rules">Rules</strong>, <span key="rules-purpose">The checks you define, with an enable toggle and one-click evaluation (see <AppLink href="/guide?section=rules">Rules & templates</AppLink>)</span>],
						[<strong key="alerts">Alerts</strong>, <span key="alerts-purpose">The inbox of breaks to triage and resolve (see <AppLink href="/guide?section=alerts">Alerts & resolution</AppLink>)</span>],
						[<strong key="insights">Insights</strong>, <span key="insights-purpose">Deviation, cumulative drift, and break distribution built from reconciliation history (see <AppLink href="/guide?section=insights">Insights</AppLink>)</span>],
					]}
				/>

				<SubHeading id="v3-rec-first-run">A practical first reconciliation</SubHeading>
				<ol className="list-decimal space-y-1 pl-7 text-sm">
					<li>Open <strong>Rules</strong>, select <strong>New rule</strong>, and choose the template card that describes the control you need.</li>
					<li>Use <strong>Load example</strong>{' '}to see a complete configuration, then replace the sample ledgers, account selectors, assets, and amounts.</li>
					<li>Choose the <strong>alert grouping period</strong>{' '}and <strong>run mode</strong>. These answer different questions: how failures share an alert, and what triggers an evaluation.</li>
					<li>Confirm each account set or source matches exactly what you expect before creating the rule.</li>
					<li>Turn monitoring <strong>On</strong>{' '}and select <strong>Run now</strong>. The latest result appears in the rule header.</li>
					<li>If it fails, select the result to open the alert, inspect the evidence, and record the operational decision.</li>
				</ol>

				<Callout kind="tip" title="Navigation behaves like the rest of the app">
					Rule names and alert fingerprints open their detail screens. The in-page back arrow returns to the
					list, and the browser Back button also restores the previous Reconcile tab, filter, or detail view.
				</Callout>

				<Callout kind="info" title="Human-first by design">
					Reconcile never silently &quot;fixes&quot; your books. It surfaces a break with its evidence and
					leaves the decision (correct it, accept it, or wait) to a person, keeping an auditable record of
					who did what and why.
				</Callout>
			</Section>
		),
	},
	{
		id: 'rules',
		group: 'Reconciliation',
		label: 'Rules & templates',
		icon: <ListChecks className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="rules"
				eyebrow="Reconciliation"
				title="Rules & templates"
				intro="A rule is an operational control you want to keep true. The Rules tab shows each control as a distinct card: its human-readable comparison, latest result, monitoring state, and Run now action. Select the card header to open its current configuration and combined activity timeline."
			>
				<SubHeading id="v3-rec-templates">Templates</SubHeading>
				<GuideTable
					caption="Rule templates"
					columns={['Template', 'Use it to…', 'Typical setup']}
					rows={[
						[<strong key="invariant">Ledger invariant</strong>, 'prove that accounting account sets net to zero', 'Debit-normal assets against credit-normal liabilities, per asset'],
						[<strong key="parity">Source parity</strong>, 'prove that two independently sourced balances agree', 'Posting-derived ledger balance against another ledger or an account-metadata balance'],
						[<strong key="threshold">Account threshold</strong>, 'detect when balances leave an allowed range', 'A minimum reserve, maximum exposure, or both, aggregated or per account'],
							[<strong key="multi">Multi-source (V2)</strong>, 'reconcile more than two named sources at once', 'A balance equation, exchange-rate bounds, source consensus, or a coverage ratio across named sources'],
					]}
				/>

				<SubHeading id="v3-rec-create">Creating a rule</SubHeading>
				<P>
					Click <strong>New rule</strong>. The template cards explain the control before you select it, and
					the form then adapts: account sets for an invariant, Source A and Source B for parity, or bounds
					for a threshold. <strong>Load example</strong>{' '}fills in a complete configuration you can inspect
					and replace. Give the rule an operational name and choose a severity that reflects the response
					it should trigger.
				</P>
				<ul className="list-disc space-y-1 pl-5 text-sm">
					<li><strong>Amounts are integers in the asset&apos;s minor units</strong> (for example, cents). A tolerance of <Code>0</Code> means an exact match.</li>
					<li><strong>Accounts are chosen by an address selector.</strong>{' '}A trailing <Code>*</Code> matches by prefix (<Code>assets:cash:*</Code>); without it the address matches exactly.</li>
					<li><strong>Metadata conditions</strong>{' '}narrow a source further: pick an indexed account-metadata key and an operator suited to its type. Use <em>equals</em> / <em>exists</em> for text and booleans, plus <em>&lt;</em> <em>≤</em> <em>&gt;</em> <em>≥</em> <em>between</em> for numbers and datetimes. Indexed keys are offered live, read through the reconciliation service&apos;s ledger connection.</li>
				</ul>

				<SubHeading id="v3-rec-source-parity">Configure source parity without reading CEL</SubHeading>
				<P>
					Configure each side by answering three questions: <strong>which ledger</strong>, <strong>which
					accounts</strong>, and <strong>which value to read</strong>. A posting-derived source uses the
					Formance balance calculated from postings. An account-metadata source reads an integer already
					stored on the account object; it does not calculate a balance from postings.
				</P>
				<GuideTable
					caption="Reading the Source A ↔ Source B comparison"
					columns={['UI element', 'What it tells you']}
					rows={[
						[<strong key="source-card">Source card</strong>, 'Ledger, account selector, value read, and—for metadata—the asset represented by that stored integer'],
						[<strong key="exact">= Exact match</strong>, 'Every configured tolerance is zero; any difference opens a break'],
						[<strong key="tolerated">≈ Within tolerance</strong>, 'At least one non-zero tolerance is allowed; a difference opens a break only outside that band'],
						[<strong key="scope">Scope badge</strong>, 'Whether matching accounts are combined or compared separately. Metadata-backed sources are aggregate-only'],
						[<strong key="tolerance">Tolerance badge</strong>, 'The allowed deviation for each asset, expressed in that asset’s minor units'],
						[<strong key="mapping-warning">Asset mapping warning</strong>, 'A metadata key represents a different asset from the configured tolerance; review the mapping before relying on the rule'],
					]}
				/>
				<P>
					Choose <strong>one metadata key</strong>{' '}and independently state the <strong>one asset</strong>{' '}
					represented by its integer value, such as <Code>account.metadata[&quot;value_known.toto&quot;]</Code> →{' '}
					<Code>USD/2</Code>. The key may contain the asset name, as in <Code>reported.USD</Code>, but it does
					not have to. Other assets may exist on the selected account; this rule checks only the declared
					asset. Configure another rule for each additional metadata-key/asset pair. The account-selection
					metadata filters above remain separate: they choose accounts, while this key supplies the value
					being reconciled. Generated CEL remains available under <strong>Technical expression</strong>, but
					it is a secondary implementation detail.
				</P>

				<SubHeading id="v3-rec-invariant-threshold">Configure invariants and thresholds</SubHeading>
				<ul className="list-disc space-y-1 pl-5 text-sm">
					<li><strong>Ledger invariant:</strong>{' '}assign each account set its accounting normal balance. Debit-normal balances are normalized as negative and credit-normal balances as positive, so equal balances on opposite sides cancel to zero. Add as many account sets as the control needs.</li>
					<li><strong>Account threshold:</strong>{' '}choose aggregate mode to check the sum of the selected set, or per-account mode to open a distinct break for each account. Add a minimum, maximum, or both for every relevant asset.</li>
				</ul>

				<SubHeading id="v3-rec-timing">Choose the alert period and run mode</SubHeading>
				<P>
					These controls are intentionally separate. The <strong>alert grouping period</strong>{' '}defines the
					lifecycle of an alert thread; <strong>run mode</strong>{' '}defines when a new observation is made.
				</P>
				<GuideTable
					caption="Alert grouping and evaluation timing"
					columns={['Setting', 'Practical effect']}
					rows={[
						[<strong key="continuous">Continuous</strong>, 'Matching failed observations keep returning to one ongoing alert thread; there is no period rollover'],
						[<strong key="periodic">Daily / weekly / monthly</strong>, 'Within the active period, matching observations share one alert that can be resolved and reopened. After rollover, the next matching failure creates a new alert'],
						[<strong key="on-demand">On demand</strong>, 'The rule evaluates only when someone selects Run now'],
						[<strong key="automatic">Automatic schedule</strong>, 'A preset or custom cron expression triggers evaluations in the configured time zone'],
					]}
				/>
				<Callout kind="info" title="Period does not mean evaluation frequency">
					A daily alert period does not necessarily mean one evaluation per day. You can evaluate many
					times during that day; matching failures become observations on the same daily alert thread.
				</Callout>

				<SubHeading id="v3-rec-run">Running and toggling</SubHeading>
				<P>
					<strong>Run now</strong>{' '}in the list—or <strong>Evaluate now</strong>{' '}in rule details—runs a rule immediately and reports <em>Pass</em>, <em>Fail</em>, or{' '}
					<em>Error</em>. A pass or fail is recorded as a <AppLink href="/guide?section=activity">capture</AppLink>; a fail
					also opens or updates an <AppLink href="/guide?section=alerts">alert</AppLink>. The <strong>On / Off</strong>{' '}
					switch turns monitoring on or off without deleting the rule. Run now stays in place while the
					switch updates, and is unavailable while the rule is off. Results settle a moment after an
					evaluation, so views refresh themselves briefly rather than assuming an instant update.
				</P>
				<P>
					A failed latest-result badge is a shortcut to the alert raised by that run. Select the rule name
					or the rest of its header to open rule details instead.
				</P>

				<SubHeading id="v3-rec-manage">Editing, duplicating & finding rules</SubHeading>
				<ul className="list-disc space-y-1 pl-5 text-sm">
					<li>Open a rule, then use <strong>Edit</strong>{' '}to change its name, configuration, or severity. The compiled CEL is regenerated from the human-readable form, and the alert period is fixed after creation.</li>
					<li><strong>Duplicate</strong>{' '}is beside Edit in rule details and starts a new rule prefilled from the existing one.</li>
					<li>Use search, template, severity, monitoring-status, and sort controls to narrow the list. Pagination appears when more than one page of matching rules exists.</li>
				</ul>

				<Callout kind="tip" title="Invalid configuration">
					If a rule&apos;s configuration is rejected (an unsupported operator, or a metadata key that
					isn&apos;t indexed on the ledger), the service names the offending field and the form shows the
					message inline. Fix it and try again.
				</Callout>
			</Section>
		),
	},
	{
		id: 'activity',
		group: 'Reconciliation',
		label: 'Rule activity',
		icon: <History className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="activity"
				eyebrow="Reconciliation"
				title="Rule activity"
				intro="Open a rule to see its current applied configuration followed by one durable activity timeline: configuration revisions, evaluation receipts, and the alert lifecycle they caused."
			>
				<SubHeading id="v3-rec-rule-detail">Use rule details as the control record</SubHeading>
				<P>
					Select a rule&apos;s header to open it. The top of the page restates the configured control in the
					same human-readable format used in the Rules list. Source-parity rules show Source A and Source B,
					the scope, and either <strong>= Exact match</strong>{' '}or <strong>≈ Within tolerance</strong>.
					Edit, Duplicate, and Evaluate now live together in the detail header. Named sources are shown before
					the invariant, and generated CEL is collapsed under <strong>Implementation details</strong>.
				</P>
				<P>
					One <strong>Combined history</strong>{' '}then follows in the exact order recorded by the backend
					journal. Captures are the evaluation receipts within that wider control record; the page does not
					reconstruct history from current alerts or older capture lists.
				</P>

				<SubHeading id="v3-rec-timeline">Reading the timeline</SubHeading>
				<P>
					Activity is listed newest first. Each evaluation shows its result (<em>pass</em>, <em>fail</em>, or
					<em> error</em>), whether it was triggered manually or on schedule, its period, duration, and the
					rule revision it used. A failing evaluation receipt carries the{' '}
					<strong>evidence</strong>{' '}behind the verdict: for a parity check, the balance on each side, their
					difference, and the tolerance; for a threshold, the balance and the bound it crossed.
				</P>
				<P>
					Alert consequences carrying the same <strong>evaluation id</strong> are grouped with that evaluation
					without changing their journal sequence. Manual alert actions have no evaluation correlation and
					remain standalone entries, making the distinction between automated consequences and operator work clear.
				</P>

				<SubHeading id="v3-rec-captures-vs">Revisions and history coverage</SubHeading>
				<P>
					Every saved configuration has a revision. Past evaluations stay tied to the revision actually used,
					and a warning appears when it differs from the current revision. Creation, update, and deletion
					entries can expose their complete snapshots; field differences are shown only when both adjacent
					snapshots have been loaded.
				</P>

				<Callout kind="info" title="The journal is forward-only">
					Activity recorded before the journal was introduced is not inferred from current state. When earlier
					lifecycle events were never recorded, the timeline names the date from which durable activity is available.
					A passing evaluation receipt remains valuable evidence that the control held.
				</Callout>
			</Section>
		),
	},
	{
		id: 'alerts',
		group: 'Reconciliation',
		label: 'Alerts & resolution',
		icon: <Bell className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="alerts"
				eyebrow="Reconciliation"
				title="Alerts & resolution"
				intro="When a rule fails it opens an alert, a break to work. The Alerts tab is the inbox: triage by status and severity, read the evidence, and record how each break was handled."
			>
				<SubHeading id="v3-rec-inbox">The inbox</SubHeading>
				<P>
					The inbox opens on <strong>All</strong>{' '}alerts and can be narrowed to <strong>Open</strong>,{' '}
					<strong>Acknowledged</strong>, or <strong>Resolved</strong>; each status filter includes a count.
					The Overview shows only recent open alerts, while its <strong>All alerts</strong>{' '}link returns here
					without narrowing the inbox. The catalogue filters follow the Rules list: search by fingerprint or
					rule name, narrow by rule, severity, or status, then sort by latest, oldest, severity, or observation
					count. Selection, grouping, refresh, and bulk actions sit on a separate operations row so they are
					not confused with filters. Each compact card has a header for severity, status, rule, and recurrence,
					followed by a human-readable comparison from the latest observation. Select the fingerprint
					or open header space to view alert details; select the <strong>rule name</strong>{' '}to open that
					rule&apos;s <AppLink href="/guide?section=activity">combined activity timeline</AppLink>.
				</P>
				<ul className="list-disc space-y-1 pl-5 text-sm">
					<li><strong>Selection-based triage:</strong>{' '}tick one or more actionable cards and the labelled acknowledge, resolve, accept, and snooze controls appear in the operations row. This is also the bulk workflow.</li>
					<li><strong>Resolved alerts:</strong>{' '}remain available for inspection but cannot be selected because there is no pending triage action to apply.</li>
					<li><strong>Group by rule:</strong>{' '}creates a collapsible section for each rule on the current page. Its own checkbox selects only that rule&apos;s visible alerts, which is useful when ownership follows the control.</li>
					<li><strong>Auto-refresh:</strong>{' '}polls every ten seconds for new and changed breaks. Leave it off when you need a stable selection for careful bulk work.</li>
				</ul>

				<SubHeading id="v3-rec-occurrences">Occurrences and evidence</SubHeading>
				<P>
					Within the active alert period, another failed observation with the same fingerprint returns to
					the same alert: its <strong>occurrence count</strong>{' '}increases and the latest evidence is updated.
					The alert may be resolved and later reopened while that period is active. When a daily, weekly,
					or monthly period rolls over, the next matching failure starts a new alert thread. Continuous
					rules have no rollover and keep one ongoing thread.
				</P>

				<SubHeading id="v3-rec-what-drove">What drove the break</SubHeading>
				<P>
					Alert details use one unified <strong>Evaluation history</strong>. The latest evaluation is
					highlighted first with its verdict, trigger, time, evaluation id, and evidence. Earlier captures
					whose evidence includes the same fingerprint are collapsed underneath; expand one to compare how
					the numbers changed. The header also links back to the rule and shows first seen, last seen, and
					the occurrence count.
				</P>
				<Callout kind="info" title="Captures are evaluation receipts">
					The rule&apos;s <AppLink href="/guide?section=activity">combined activity timeline</AppLink> is the canonical control
					record. Capture evidence remains the receipt for an individual evaluation, while the backend journal
					records configuration and alert lifecycle activity around it.
				</Callout>

				<SubHeading id="v3-rec-actions">Working a break</SubHeading>
				<GuideTable
					caption="Resolution actions"
					columns={['Action', 'Use it when', 'Effect']}
					rows={[
						[<strong key="acknowledge">Acknowledge</strong>, 'You are investigating', 'Marks the break as being worked; notifications keep flowing'],
						[<strong key="resolve">Resolve</strong>, 'The break is fixed', 'Closes it; adding booking transaction references records it as fixed by booking'],
						[<strong key="accept">Accept</strong>, 'It is a known, tolerated difference', 'Closes it as accepted by the business (a note is required)'],
						[<strong key="snooze">Snooze / Unsnooze</strong>, 'You are waiting on something (e.g. a bank file)', 'Mutes notifications until a future time you set, then lifts the mute'],
					]}
				/>
				<P>
					You can act from alert details or select one or more alerts in the inbox to reveal the same labelled
					actions. The action form shows how many alerts will be changed. Every action records the
					actor and an optional note; acceptance requires a reason, resolve can include comma-separated
					booking transaction references, and snooze requires a future time. Alert details show these
					decisions together under <strong>Lifecycle</strong>.
				</P>

				<Callout kind="tip" title="Resolve vs. accept">
					<strong>Resolve</strong>{' '}says &quot;the books were corrected&quot; (ideally with the booking
					reference that fixed them). <strong>Accept</strong>{' '}says &quot;this difference is expected and we
					are choosing to live with it.&quot; Both close the break, but they tell very different stories
					later.
				</Callout>
			</Section>
		),
	},
	{
		id: 'insights',
		group: 'Reconciliation',
		label: 'Insights',
		icon: <LineChart className="size-4" aria-hidden />,
		render: () => (
			<Section
				id="insights"
				eyebrow="Reconciliation"
				title="Reconciliation insights"
				intro="Insights turns capture and alert history into operational signals. Use it after rules have run several times: the charts help separate isolated breaks from persistent bias and show where the alert workload is concentrated."
			>
				<SubHeading id="v3-rec-insights-start">Start with a rule and asset</SubHeading>
				<ol className="list-decimal space-y-1 pl-7 text-sm">
					<li>Choose a rule from the selector. Source-parity and account-threshold rules provide deviation charts.</li>
					<li>If the rule covers several assets or fingerprints, choose the series you want to investigate.</li>
					<li>Hover a point for its exact time, verdict, observed value, and configured boundary.</li>
					<li>Return to the rule&apos;s combined activity timeline when you need the complete evidence for a specific run.</li>
				</ol>

				<SubHeading id="v3-rec-insights-charts">Read the charts</SubHeading>
				<GuideTable
					caption="Insights charts"
					columns={['Chart', 'How to use it']}
					rows={[
						[<strong key="deviation">Deviation over time</strong>, 'For source parity, plots the signed difference against the ± tolerance band. For thresholds, plots the balance against its minimum and maximum'],
						[<strong key="passes">Pass markers</strong>, 'Show that a run stayed safe. A pass capture does not record a magnitude, so the marker is placed inside the safe zone without inventing a value'],
						[<strong key="drift">Cumulative drift</strong>, 'Adds the signed gap over time. A line that keeps rising or falling indicates persistent one-sided bias; movement that returns toward zero behaves more like offsetting noise'],
						[<strong key="breaks">Breaks by rule type</strong>, 'Shows the global alert count split by template and status, helping identify which class of control creates the most operational work'],
					]}
				/>

				<Callout kind="info" title="What Insights does not invent">
					Failed captures contain the measured deviation and can be plotted precisely. Passing captures
					prove that the control held, but may not store the exact safe value; the chart therefore shows a
					pass marker rather than fabricating a measurement.
				</Callout>

				<Callout kind="tip" title="No chart yet?">
					Evaluate the rule from its detail screen to create an evaluation receipt in rule activity. Ledger-invariant rules still
					contribute to break distribution, but their normalized sum is not currently plotted in the
					deviation chart.
				</Callout>
			</Section>
		),
	},
]
