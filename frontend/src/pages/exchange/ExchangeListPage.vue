<template>
  <el-card>
    <template #header>
      <div class="head">
        <span>换物提案</span>
      </div>
    </template>

    <el-radio-group v-model="roleFilter" class="role-filter" @change="load(1)">
      <el-radio-button value="recipient">收到的</el-radio-button>
      <el-radio-button value="initiator">我发起的</el-radio-button>
      <el-radio-button value="">全部</el-radio-button>
    </el-radio-group>

    <el-tabs v-model="statusFilter" class="status-tabs" @tab-change="load(1)">
      <el-tab-pane label="全部" name="" />
      <el-tab-pane label="待回应" name="pending" />
      <el-tab-pane label="已还价" name="countered" />
      <el-tab-pane label="已接受" name="accepted" />
      <el-tab-pane label="已拒绝" name="rejected" />
      <el-tab-pane label="已取消" name="cancelled" />
      <el-tab-pane label="已超时" name="expired" />
    </el-tabs>

    <div v-loading="loading">
      <el-card v-for="p in proposals" :key="p.id" class="p-card" shadow="never" @click="goDetail(p.id)">
        <div class="p-head">
          <span class="no">{{ p.proposal_no }}</span>
          <StatusBadge type="exchange" :value="p.status" />
        </div>
        <div class="p-body">
          <div class="side-group">
            <div class="side-label">换出</div>
            <div class="items">
              <el-tag v-for="i in p.offer_items" :key="i.id" size="small" class="item-tag">{{ i.title }}</el-tag>
            </div>
          </div>
          <el-icon class="arrow"><Right /></el-icon>
          <div class="side-group">
            <div class="side-label">换入</div>
            <div class="items">
              <el-tag v-for="i in p.target_items" :key="i.id" size="small" type="success" class="item-tag">{{ i.title }}</el-tag>
            </div>
          </div>
        </div>
        <div class="p-foot">
          <span class="topup" v-if="p.top_up_amount > 0">
            补差价 ¥{{ formatPrice(p.top_up_amount) }}（{{ p.top_up_payer === 'offeror' ? '发起人支付' : '接收人支付' }}）
          </span>
          <span v-else class="topup">等价交换，无补差价</span>
          <span class="time">{{ p.created_at }}</span>
        </div>
        <div v-if="isMyTurn(p)" class="turn-tip">
          <el-tag type="warning" size="small">轮到我回应</el-tag>
        </div>
      </el-card>
      <EmptyState v-if="!loading && proposals.length === 0" description="暂无换物提案" />
    </div>

    <div class="pager">
      <el-pagination background layout="prev, pager, next, total" :total="total" :page-size="pageSize" :current-page="page" @current-change="load" />
    </div>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Right } from '@element-plus/icons-vue'
import * as exchangeApi from '../../api/exchangeProposal'
import { useUserStore } from '../../stores/userStore'
import StatusBadge from '../../components/StatusBadge.vue'
import EmptyState from '../../components/EmptyState.vue'
import { formatPrice } from '../../utils/format'
import type { ExchangeProposalVO } from '../../api/types'

const router = useRouter()
const userStore = useUserStore()

const proposals = ref<ExchangeProposalVO[]>([])
const loading = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = 10
const roleFilter = ref('recipient')
const statusFilter = ref('')

onMounted(() => load(1))

async function load(p: number) {
  loading.value = true
  try {
    const params: Record<string, unknown> = { page: p, page_size: pageSize }
    if (roleFilter.value) params.role = roleFilter.value
    if (statusFilter.value) params.status = statusFilter.value
    const res: any = await exchangeApi.listExchangeProposals(params)
    proposals.value = res.data.list || []
    total.value = Number(res.data.total || 0)
    page.value = p
  } finally {
    loading.value = false
  }
}

function goDetail(id: number) {
  router.push(`/exchange/${id}`)
}

function isMyTurn(p: ExchangeProposalVO): boolean {
  const myId = userStore.user?.id || 0
  if (p.status !== 'pending' && p.status !== 'countered') return false
  return (p.turn === 'offeror' && p.offeror_id === myId) || (p.turn === 'offeree' && p.offeree_id === myId)
}
</script>

<style scoped>
.head {
  font-weight: 600;
}
.role-filter {
  margin-bottom: 8px;
}
.p-card {
  margin-bottom: 12px;
  cursor: pointer;
}
.p-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-bottom: 8px;
  border-bottom: 1px dashed #ebeef5;
}
.no {
  color: #909399;
  font-size: 13px;
}
.p-body {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 0;
}
.side-group {
  flex: 1;
}
.side-label {
  color: #909399;
  font-size: 12px;
  margin-bottom: 6px;
}
.items {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.item-tag {
  margin: 0;
}
.arrow {
  color: #c0c4cc;
  font-size: 18px;
}
.p-foot {
  display: flex;
  justify-content: space-between;
  color: #909399;
  font-size: 13px;
}
.topup {
  color: #f56c6c;
}
.turn-tip {
  margin-top: 8px;
}
.pager {
  display: flex;
  justify-content: center;
  margin-top: 16px;
}
</style>
