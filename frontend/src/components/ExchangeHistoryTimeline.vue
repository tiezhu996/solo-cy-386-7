<template>
  <el-timeline class="exchange-timeline">
    <el-timeline-item
      v-for="h in history"
      :key="h.id"
      :timestamp="h.created_at"
      placement="top"
      :type="dotType(h.action)"
    >
      <div class="row">
        <span class="actor">{{ actorName(h) }}</span>
        <el-tag size="small" :type="tagType(h.action)" effect="plain">{{ actionText(h.action) }}</el-tag>
      </div>
      <div v-if="h.note" class="note">{{ h.note }}</div>
      <div v-if="h.action === 'created' || h.action === 'countered'" class="terms">
        补差价：¥{{ formatPrice(h.top_up_amount) }}（{{ h.top_up_payer === 'offeror' ? '发起人支付' : '接收人支付' }}）
      </div>
    </el-timeline-item>
  </el-timeline>
</template>

<script setup lang="ts">
import type { ExchangeHistoryVO } from '../api/types'
import { formatPrice } from '../utils/format'
import { ExchangeActionText, ExchangePartyText } from '../constants'

const props = defineProps<{ history: ExchangeHistoryVO[] }>()

function actorName(h: ExchangeHistoryVO): string {
  if (h.actor?.nickname) return h.actor.nickname
  return ExchangePartyText[h.actor_role] ?? '用户'
}

function actionText(action: string): string {
  return ExchangeActionText[action] ?? action
}

function tagType(action: string): any {
  switch (action) {
    case 'accepted':
      return 'success'
    case 'rejected':
    case 'cancelled':
    case 'expired':
      return 'info'
    case 'countered':
      return 'primary'
    default:
      return 'warning'
  }
}

function dotType(action: string): any {
  switch (action) {
    case 'accepted':
      return 'success'
    case 'rejected':
    case 'cancelled':
    case 'expired':
      return 'info'
    case 'countered':
      return 'primary'
    default:
      return 'warning'
  }
}
</script>

<style scoped>
.row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.actor {
  font-weight: 600;
  color: #303133;
}
.note {
  color: #606266;
  margin-top: 4px;
  white-space: pre-wrap;
}
.terms {
  color: #909399;
  font-size: 12px;
  margin-top: 4px;
}
</style>
