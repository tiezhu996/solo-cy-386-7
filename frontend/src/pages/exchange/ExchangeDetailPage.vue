<template>
  <div v-loading="loading">
    <el-card v-if="proposal">
      <template #header>
        <div class="head">
          <div>
            <span class="no">换物提案 {{ proposal.proposal_no }}</span>
            <StatusBadge type="exchange" :value="proposal.status" class="badge" />
          </div>
          <el-button text @click="$router.push('/exchange')">返回列表</el-button>
        </div>
      </template>

      <el-descriptions :column="2" border>
        <el-descriptions-item label="发起人">
          {{ proposal.offeror?.nickname || `用户${proposal.offeror_id}` }}
          <el-tag v-if="isOfferor" size="small" type="warning">我</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="接收人">
          {{ proposal.offeree?.nickname || `用户${proposal.offeree_id}` }}
          <el-tag v-if="isOfferee" size="small" type="success">我</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="补差价">
          ¥{{ formatPrice(proposal.top_up_amount) }}
          <span class="muted">（{{ proposal.top_up_payer === 'offeror' ? '发起人支付给接收人' : '接收人支付给发起人' }}）</span>
        </el-descriptions-item>
        <el-descriptions-item label="有效期至">{{ proposal.expires_at }}</el-descriptions-item>
        <el-descriptions-item v-if="proposal.note" label="说明" :span="2">{{ proposal.note }}</el-descriptions-item>
      </el-descriptions>

      <el-row :gutter="20" class="items">
        <el-col :span="12">
          <h3 class="col-title">换出物品（{{ proposal.offer_items.length }}）</h3>
          <ExchangeItemCard
            v-for="item in proposal.offer_items"
            :key="'o' + item.id"
            :item="item"
            show-side
            class="link-item"
            @click="$router.push(`/products/${item.product_id}`)"
          />
        </el-col>
        <el-col :span="12">
          <h3 class="col-title">换入物品（{{ proposal.target_items.length }}）</h3>
          <ExchangeItemCard
            v-for="item in proposal.target_items"
            :key="'t' + item.id"
            :item="item"
            show-side
            class="link-item"
            @click="$router.push(`/products/${item.product_id}`)"
          />
        </el-col>
      </el-row>

      <!-- 动作区：按钮显隐严格对齐后端状态机/回合（非参与方不展示任何动作） -->
      <div v-if="canAct" class="actions">
        <template v-if="myTurn">
          <el-button type="primary" :loading="acting" @click="onAccept">接受提案</el-button>
          <el-button v-if="canCounter" type="warning" @click="openCounter">还价</el-button>
          <el-button v-if="isOfferee" @click="openReject">拒绝</el-button>
        </template>
        <el-button v-if="isOfferor" @click="openCancel">取消提案</el-button>
        <span v-if="active && !myTurn" class="waiting">等待{{ proposal.turn === 'offeror' ? '发起人' : '接收人' }}回应…</span>
      </div>
    </el-card>

    <el-card v-if="proposal" class="history-card">
      <template #header>完整历史</template>
      <ExchangeHistoryTimeline :history="proposal.history" />
    </el-card>

    <!-- 还价弹窗 -->
    <el-dialog v-model="counterDialog" title="还价（仅可还价一次）" width="420px">
      <el-form label-width="100px">
        <el-form-item label="补差价金额">
          <el-input-number v-model="counterForm.top_up_amount" :min="0" :precision="2" :step="50" />
        </el-form-item>
        <el-form-item label="差价支付方">
          <el-radio-group v-model="counterForm.top_up_payer">
            <el-radio value="offeror">发起人支付</el-radio>
            <el-radio value="offeree">接收人支付</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="counterForm.note" type="textarea" :rows="3" maxlength="500" show-word-limit />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="counterDialog = false">取消</el-button>
        <el-button type="primary" :loading="acting" @click="onCounter">提交还价</el-button>
      </template>
    </el-dialog>

    <!-- 拒绝/取消确认 -->
    <el-dialog v-model="noteDialog" :title="noteDialogTitle" width="420px">
      <el-input v-model="noteForm" type="textarea" :rows="3" maxlength="500" placeholder="可填写附言（可选）" />
      <template #footer>
        <el-button @click="noteDialog = false">取消</el-button>
        <el-button :type="noteAction === 'reject' ? 'danger' : 'default'" :loading="acting" @click="confirmNote">确定</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as exchangeApi from '../../api/exchangeProposal'
import { useUserStore } from '../../stores/userStore'
import StatusBadge from '../../components/StatusBadge.vue'
import ExchangeItemCard from '../../components/ExchangeItemCard.vue'
import ExchangeHistoryTimeline from '../../components/ExchangeHistoryTimeline.vue'
import { formatPrice } from '../../utils/format'
import type { ExchangeProposalVO } from '../../api/types'

const route = useRoute()
const userStore = useUserStore()

const proposal = ref<ExchangeProposalVO | null>(null)
const loading = ref(false)
const acting = ref(false)

const counterDialog = ref(false)
const counterForm = ref({ top_up_amount: 0, top_up_payer: 'offeror' as 'offeror' | 'offeree', note: '' })

const noteDialog = ref(false)
const noteDialogTitle = ref('')
const noteAction = ref<'reject' | 'cancel'>('reject')
const noteForm = ref('')

const myId = computed(() => userStore.user?.id || 0)
const isOfferor = computed(() => proposal.value?.offeror_id === myId.value)
const isOfferee = computed(() => proposal.value?.offeree_id === myId.value)
const active = computed(() => proposal.value?.status === 'pending' || proposal.value?.status === 'countered')
const isParty = computed(() => isOfferor.value || isOfferee.value)
const myRole = computed(() => (isOfferor.value ? 'offeror' : isOfferee.value ? 'offeree' : ''))
const myTurn = computed(() => active.value && proposal.value?.turn === myRole.value)
const canAct = computed(() => isParty.value && active.value)
// 仅初始 pending（接收人回合）可还价；countered 已是还价后的终前态，不能再次还价。
const canCounter = computed(() => active.value && myTurn.value && proposal.value?.status === 'pending' && isOfferee.value)

onMounted(load)

async function load() {
  loading.value = true
  try {
    const res: any = await exchangeApi.getExchangeProposal(Number(route.params.id))
    proposal.value = res.data
  } finally {
    loading.value = false
  }
  if (proposal.value) {
    counterForm.value = {
      top_up_amount: Number(proposal.value.top_up_amount || 0),
      top_up_payer: proposal.value.top_up_payer,
      note: proposal.value.note || ''
    }
  }
}

async function onAccept() {
  try {
    await ElMessageBox.confirm('接受后双方物品将立即锁定成交，涉及这些物品的其他提案会自动结束。确认接受？', '确认接受', {
      type: 'warning',
      confirmButtonText: '确认接受',
      cancelButtonText: '再想想'
    })
  } catch {
    return
  }
  acting.value = true
  try {
    await exchangeApi.acceptExchangeProposal(proposal.value!.id)
    ElMessage.success('已接受，物品锁定成交')
    await load()
  } catch (err) {
    // 到期接受会在后端完成超时收口并返回失败：刷新以呈现 expired 状态与历史。
    await load()
    throw err
  } finally {
    acting.value = false
  }
}

function openCounter() {
  counterDialog.value = true
}

async function onCounter() {
  acting.value = true
  try {
    await exchangeApi.counterExchangeProposal(proposal.value!.id, { ...counterForm.value })
    counterDialog.value = false
    ElMessage.success('已还价，等待对方回应')
    await load()
  } catch (err) {
    await load()
    throw err
  } finally {
    acting.value = false
  }
}

function openReject() {
  noteAction.value = 'reject'
  noteDialogTitle.value = '拒绝提案'
  noteForm.value = ''
  noteDialog.value = true
}

function openCancel() {
  noteAction.value = 'cancel'
  noteDialogTitle.value = '取消提案'
  noteForm.value = ''
  noteDialog.value = true
}

async function confirmNote() {
  acting.value = true
  try {
    if (noteAction.value === 'reject') {
      await exchangeApi.rejectExchangeProposal(proposal.value!.id, noteForm.value)
      ElMessage.success('已拒绝')
    } else {
      await exchangeApi.cancelExchangeProposal(proposal.value!.id, noteForm.value)
      ElMessage.success('已取消')
    }
    noteDialog.value = false
    await load()
  } catch (err) {
    await load()
    throw err
  } finally {
    acting.value = false
  }
}
</script>

<style scoped>
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.no {
  font-weight: 600;
  margin-right: 10px;
}
.badge {
  margin-left: 4px;
}
.muted {
  color: #909399;
  font-size: 12px;
}
.items {
  margin-top: 18px;
}
.col-title {
  font-size: 15px;
  margin: 0 0 10px;
  color: #303133;
}
.link-item :deep(.ex-item) {
  cursor: pointer;
}
.actions {
  margin-top: 20px;
  display: flex;
  align-items: center;
  gap: 10px;
}
.waiting {
  color: #909399;
  font-size: 13px;
}
.history-card {
  margin-top: 16px;
}
</style>
