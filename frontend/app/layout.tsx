import type { Metadata } from 'next'
import { Suspense } from 'react'
import { Toaster } from 'sonner'

import './globals.css'
import { ThemeProvider } from '@/components/theme-provider'
import { AppShell } from '@/components/app-shell'

export const metadata: Metadata = {
	title: 'Reconciliation · Ledger Clarity',
	description: 'Business-facing reconciliation view for the Reconciliation module on Formance Ledger v3',
}

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
	return (
		<html lang="en" suppressHydrationWarning>
			<body className="antialiased">
				<ThemeProvider attribute="class" defaultTheme="light" enableSystem disableTransitionOnChange>
					<Suspense fallback={null}>
						<AppShell>{children}</AppShell>
					</Suspense>
					<Toaster richColors position="top-right" />
				</ThemeProvider>
			</body>
		</html>
	)
}
