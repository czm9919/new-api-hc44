import { createFileRoute } from '@tanstack/react-router'

import { ContributeKey } from '@/features/key-pool'

export const Route = createFileRoute('/_authenticated/contribute-key/')({
  component: ContributeKey,
})
