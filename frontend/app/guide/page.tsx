import { Suspense } from 'react'
import { GuideShell } from '@/components/guide/guide-shell'

export default function GuidePage() {
	return (
		<div className="h-full overflow-y-auto">
			<Suspense fallback={null}>
				<GuideShell />
			</Suspense>
		</div>
	)
}
