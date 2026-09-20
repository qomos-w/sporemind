import { createContext, useContext } from 'react'
import type { InspectRef } from '../../gen-clients/system/types'

export type InspectFn = (ref: InspectRef) => void

export const InspectContext = createContext<InspectFn | undefined>(undefined)

export function useInspect(): InspectFn | undefined {
  return useContext(InspectContext)
}
