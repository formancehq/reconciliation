'use client'

// User Guide shell: grouped section nav (left, sticky) + content (center) + an
// "On this page" TOC (right, ≥xl, sticky). The active section is driven by the
// ?section= query so in-guide cross-links and deep-links stay in sync. Mirrors
// the console shell's guide UX (the reconcile guide content moved here), styled
// with the Formance design system. Uses the natural page scroll so it behaves
// the same standalone or embedded.

import * as React from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { BookOpen } from 'lucide-react'

import { cn } from '@/lib/utils'
import { PageHeader } from '@/components/page-header'
import { prefersReducedMotion, scrollToEl } from './primitives'
import { DEFAULT_SECTION, GUIDE_SECTIONS, type GuideSection } from './content'

interface Group {
	label: string
	items: GuideSection[]
}

function groupSections(): Group[] {
	const groups: Group[] = []
	for (const s of GUIDE_SECTIONS) {
		let g = groups.find((x) => x.label === s.group)
		if (!g) {
			g = { label: s.group, items: [] }
			groups.push(g)
		}
		g.items.push(s)
	}
	return groups
}

export function GuideShell() {
	const router = useRouter()
	const search = useSearchParams()
	const requested = search.get('section') ?? DEFAULT_SECTION
	const active = GUIDE_SECTIONS.find((s) => s.id === requested)?.id ?? DEFAULT_SECTION
	const groups = React.useMemo(groupSections, [])
	const contentRef = React.useRef<HTMLDivElement>(null)

	const select = React.useCallback(
		(id: string) => router.replace(id === DEFAULT_SECTION ? '/guide' : `/guide?section=${id}`, { scroll: false }),
		[router]
	)

	// Jump to top of the page when the section changes (window is the scroller).
	React.useEffect(() => {
		if (typeof window !== 'undefined') window.scrollTo({ top: 0, behavior: prefersReducedMotion() ? 'auto' : 'smooth' })
	}, [active])

	const current = GUIDE_SECTIONS.find((s) => s.id === active) ?? GUIDE_SECTIONS[0]
	const { headings, activeId } = useHeadings(contentRef, [active])

	return (
		<div className="mx-auto max-w-7xl p-6 md:p-8">
			<PageHeader
				title={
					<span className="flex items-center gap-2.5">
						<BookOpen className="size-6" aria-hidden /> User guide
					</span>
				}
				description="How reconciliation works as a business process — observe, detect, resolve — and where to see each concept in the app."
			/>

			<div className="mt-6 flex gap-8">
				{/* Section nav */}
				<nav aria-label="User guide sections" className="hidden w-60 shrink-0 md:block">
					<div className="sticky top-6 max-h-[calc(100vh-3rem)] space-y-6 overflow-y-auto pr-3">
						{groups.map((g) => (
							<div key={g.label} className="space-y-1">
								<p className="px-2 pb-1 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
									{g.label}
								</p>
								{g.items.map((item) => {
									const isActive = item.id === active
									return (
										<button
											key={item.id}
											type="button"
											aria-current={isActive ? 'page' : undefined}
											onClick={() => select(item.id)}
											className={cn(
												'flex w-full items-center gap-2.5 border-l-2 px-3 py-1.5 text-left text-sm transition-colors',
												isActive
													? 'border-primary bg-accent font-medium text-accent-foreground'
													: 'border-transparent text-muted-foreground hover:bg-accent/60 hover:text-foreground'
											)}
										>
											<span className="shrink-0" aria-hidden>
												{item.icon}
											</span>
											<span className="truncate">{item.label}</span>
										</button>
									)
								})}
							</div>
						))}
					</div>
				</nav>

				{/* Content */}
				<div ref={contentRef} className="min-w-0 flex-1">
					{/* mobile section picker */}
					<div className="mb-5 md:hidden">
						<select
							value={active}
							onChange={(e) => select(e.target.value)}
							className="w-full border bg-background px-3 py-2 text-sm"
							aria-label="User guide section"
						>
							{groups.map((g) => (
								<optgroup key={g.label} label={g.label}>
									{g.items.map((item) => (
										<option key={item.id} value={item.id}>
											{item.label}
										</option>
									))}
								</optgroup>
							))}
						</select>
					</div>
					{current.render()}
				</div>

				{/* On this page */}
				<OnThisPage headings={headings} activeId={activeId} />
			</div>
		</div>
	)
}

interface Heading {
	id: string
	text: string
}

function useHeadings(containerRef: React.RefObject<HTMLElement | null>, deps: unknown[]) {
	const [headings, setHeadings] = React.useState<Heading[]>([])
	const [activeId, setActiveId] = React.useState<string | null>(null)

	React.useEffect(() => {
		const el = containerRef.current
		if (!el) return
		const found: Heading[] = []
		el.querySelectorAll('h3[id]').forEach((h) => found.push({ id: h.id, text: h.textContent || '' }))
		setHeadings(found)
		if (found.length === 0) {
			setActiveId(null)
			return
		}
		const observer = new IntersectionObserver(
			(entries) => {
				const visible = entries
					.filter((e) => e.isIntersecting)
					.sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
				if (visible.length > 0) setActiveId(visible[0].target.id)
			},
			{ rootMargin: '-10% 0% -70% 0%', threshold: 0 }
		)
		found.forEach((h) => {
			const el2 = document.getElementById(h.id)
			if (el2) observer.observe(el2)
		})
		return () => observer.disconnect()
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, deps)

	return { headings, activeId }
}

function OnThisPage({ headings, activeId }: { headings: Heading[]; activeId: string | null }) {
	if (headings.length < 2) return null
	return (
		<aside aria-label="On this page" className="hidden w-52 shrink-0 xl:block">
			<div className="sticky top-6">
				<p className="mb-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">On this page</p>
				<ol className="space-y-1 border-l text-sm">
					{headings.map((h) => (
						<li key={h.id}>
							<a
								href={`#${h.id}`}
								aria-current={h.id === activeId ? 'true' : undefined}
								onClick={(e) => {
									e.preventDefault()
									scrollToEl(document.getElementById(h.id))
									history.replaceState(null, '', `#${h.id}`)
								}}
								className={cn(
									'-ml-px block border-l-2 py-0.5 pl-3 transition-colors',
									h.id === activeId
										? 'border-primary font-medium text-foreground'
										: 'border-transparent text-muted-foreground hover:text-foreground'
								)}
							>
								{h.text}
							</a>
						</li>
					))}
				</ol>
			</div>
		</aside>
	)
}
