// exchangeStore.ts 换物提案状态管理（列表加载、动作封装、待回应计数）。
import { defineStore } from 'pinia'
import { ref } from 'vue'
import * as exchangeApi from '../api/exchangeProposal'

export const useExchangeStore = defineStore('exchange', () => {
  const pendingCount = ref(0)

  async function create(payload: {
    offer_product_ids: number[]
    target_product_ids: number[]
    note?: string
    top_up_amount?: number
    top_up_payer?: 'offeror' | 'offeree'
    ttl_hours?: number
  }) {
    const res: any = await exchangeApi.createExchangeProposal(payload)
    return res.data
  }

  async function accept(id: number) {
    return exchangeApi.acceptExchangeProposal(id)
  }

  async function reject(id: number, note?: string) {
    return exchangeApi.rejectExchangeProposal(id, note)
  }

  async function counter(id: number, data: { top_up_amount?: number; top_up_payer?: 'offeror' | 'offeree'; note?: string }) {
    return exchangeApi.counterExchangeProposal(id, data)
  }

  async function cancel(id: number, note?: string) {
    return exchangeApi.cancelExchangeProposal(id, note)
  }

  // loadPendingCount 统计“需要我回应”的生效提案：
  // role=initiator 且 turn=offeror（对方还价等我回应）；role=recipient 且 turn=offeree（对方发起等我回应）。
  async function loadPendingCount() {
    try {
      const [init, recv]: any[] = await Promise.all([
        exchangeApi.listExchangeProposals({ role: 'initiator', status: 'countered', page: 1, page_size: 50 }),
        exchangeApi.listExchangeProposals({ role: 'recipient', status: 'pending', page: 1, page_size: 50 })
      ])
      pendingCount.value = Number(init.data.total || 0) + Number(recv.data.total || 0)
    } catch {
      pendingCount.value = 0
    }
  }

  return { pendingCount, create, accept, reject, counter, cancel, loadPendingCount }
})
