import request from '../utils/request'
import type { ExchangeProposalVO, PageResult } from './types'

// 发起换物提案：用自己的在售物品换对方一件或多件在售物品，可补差价。
export function createExchangeProposal(data: {
  offer_product_ids: number[]
  target_product_ids: number[]
  note?: string
  top_up_amount?: number
  top_up_payer?: 'offeror' | 'offeree'
  ttl_hours?: number
}) {
  return request.post('/exchange/proposals', data)
}

export function listExchangeProposals(params: Record<string, unknown>) {
  return request.get('/exchange/proposals', { params })
}

export function getExchangeProposal(id: number) {
  return request.get(`/exchange/proposals/${id}`)
}

export function acceptExchangeProposal(id: number) {
  return request.post(`/exchange/proposals/${id}/accept`)
}

export function rejectExchangeProposal(id: number, note?: string) {
  return request.post(`/exchange/proposals/${id}/reject`, { note })
}

export function counterExchangeProposal(id: number, data: { top_up_amount?: number; top_up_payer?: 'offeror' | 'offeree'; note?: string }) {
  return request.post(`/exchange/proposals/${id}/counter`, data)
}

export function cancelExchangeProposal(id: number, note?: string) {
  return request.post(`/exchange/proposals/${id}/cancel`, { note })
}

export type { ExchangeProposalVO, PageResult }
