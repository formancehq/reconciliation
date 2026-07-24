'use client'

import * as React from 'react'
import Link from 'next/link'
import { usePathname, useSearchParams } from 'next/navigation'
import { useTheme } from 'next-themes'
import { BookOpen, Scale } from 'lucide-react'

import { Brand } from '@/components/brand'
import { ThemeToggle } from '@/components/theme-toggle'
import { reconClient } from '@/lib/recon/client'
import { cn } from '@/lib/utils'

const NAV = [
	{ title: 'Reconciliation', href: '/', icon: Scale },
	{ title: 'User guide', href: '/guide', icon: BookOpen },
]

function isActive(pathname: string, href: string) {
	if (href === '/') return pathname === '/'
	return pathname === href || pathname.startsWith(href + '/')
}

/** Standalone sidebar OR embedded top-bar, plus theme sync from ?theme / postMessage. */
export function AppShell({ children }: { children: React.ReactNode }) {
	const pathname = usePathname()
	const search = useSearchParams()
	const { setTheme } = useTheme()

	// Whether we're embedded in the console shell. Detected from the initial
	// ?embedded=1 query param, but LATCHED: internal <Link> navigation drops the
	// query, so a plain per-render read would flip the app back to its standalone
	// sidebar on the first tab click. Initialize from the query (matching the SSR
	// read) and, after mount, also treat "actually inside an iframe" as embedded.
	const [embedded, setEmbedded] = React.useState(
		() => search.get('embedded') === '1' || search.get('embed') === '1',
	)
	React.useEffect(() => {
		const framed = (() => {
			try {
				return window.self !== window.top
			} catch {
				return true // cross-origin access throwing means we're framed
			}
		})()
		if (framed || search.get('embedded') === '1' || search.get('embed') === '1') {
			setEmbedded(true)
		}
	}, [search])

	// Theme from the ?theme query param (so the console shell can force a theme
	// when embedding this UI in an iframe).
	const applied = React.useRef(false)
	React.useEffect(() => {
		const t = search.get('theme')
		if (!applied.current && (t === 'light' || t === 'dark')) {
			setTheme(t)
			applied.current = true
		}
	}, [search, setTheme])

	// Live theme + auth handoff from the parent frame (Phase 2 embedding).
	React.useEffect(() => {
		function onMessage(e: MessageEvent) {
			const msg = e.data
			if (!msg || typeof msg !== 'object') return
			if (msg.type === 'set-theme' && (msg.theme === 'light' || msg.theme === 'dark')) setTheme(msg.theme)
			if (msg.type === 'set-token' && typeof msg.token === 'string') {
				try {
					sessionStorage.setItem('reconciliation.token', msg.token)
				} catch {
					/* ignore */
				}
			}
		}
		window.addEventListener('message', onMessage)
		return () => window.removeEventListener('message', onMessage)
	}, [setTheme])

	if (embedded) {
		return (
			<div className="flex h-screen flex-col">
				<header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b bg-background/80 px-4 backdrop-blur">
					<div className="flex items-center gap-6">
						<Brand />
						<nav className="flex items-center gap-1">
							{NAV.map((item) => (
								<NavLink key={item.href} {...item} active={isActive(pathname, item.href)} horizontal />
							))}
						</nav>
					</div>
					<ThemeToggle />
				</header>
				<main className="min-h-0 flex-1 overflow-hidden">{children}</main>
			</div>
		)
	}

	return (
		<div className="flex h-screen">
			<aside className="sticky top-0 hidden h-screen w-60 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground md:flex">
				<div className="flex h-16 items-center border-b px-4">
					<Brand />
				</div>
				<nav className="flex-1 space-y-1 p-3">
					<p className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">Ledger Clarity</p>
					{NAV.map((item) => (
						<NavLink key={item.href} {...item} active={isActive(pathname, item.href)} />
					))}
				</nav>
				<SidebarFooter />
			</aside>
			<div className="flex min-h-0 min-w-0 flex-1 flex-col">
				{/* Mobile top bar */}
				<header className="flex h-14 items-center justify-between border-b px-4 md:hidden">
					<Brand />
					<ThemeToggle />
				</header>
				<main className="min-h-0 flex-1 overflow-hidden">{children}</main>
			</div>
		</div>
	)
}

function NavLink({
	href,
	title,
	icon: Icon,
	active,
	horizontal,
}: {
	href: string
	title: string
	icon: React.ComponentType<{ className?: string }>
	active: boolean
	horizontal?: boolean
}) {
	return (
		<Link
			href={href}
			className={cn(
				'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors',
				horizontal ? 'h-9' : 'w-full',
				active
					? 'bg-sidebar-accent text-sidebar-accent-foreground'
					: 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground'
			)}
		>
			<Icon className="size-4 shrink-0" />
			{title}
		</Link>
	)
}

function SidebarFooter() {
	const [ok, setOk] = React.useState<boolean | null>(null)
	React.useEffect(() => {
		let active = true
		const ping = () => reconClient.health().then((h) => active && setOk(h)).catch(() => active && setOk(false))
		ping()
		const id = setInterval(ping, 15000)
		return () => {
			active = false
			clearInterval(id)
		}
	}, [])

	return (
		<div className="border-t p-3">
			<div className="flex items-center justify-between rounded-md px-2 py-1.5">
				<div className="flex items-center gap-2 text-xs text-muted-foreground">
					<span
						className={cn(
							'size-2 rounded-full',
							ok == null ? 'bg-amber-foreground' : ok ? 'bg-green-foreground' : 'bg-red-foreground'
						)}
					/>
					{ok == null ? 'Connecting…' : ok ? 'Reconciliation backend up' : 'Backend offline'}
				</div>
				<ThemeToggle />
			</div>
		</div>
	)
}
