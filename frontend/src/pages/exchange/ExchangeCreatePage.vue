<template>
  <el-card v-loading="loading">
    <template #header>
      <div class="head">
        <span>发起换物提案</span>
        <el-button text @click="$router.back()">返回</el-button>
      </div>
    </template>

    <el-alert
      v-if="!loading && (!targetProducts.length || !myProducts.length)"
      type="warning"
      :closable="false"
      show-icon
      title="双方都需要至少一件在售物品才能发起换物"
      description="请确认对方物品在售，且你在“个人中心-我的发布”中有在售物品。"
      style="margin-bottom: 16px"
    />

    <el-row :gutter="20">
      <!-- 我拿出的物品 -->
      <el-col :span="12">
        <h3 class="col-title">我换出的物品（至少 1 件）</h3>
        <div v-if="myProducts.length === 0" class="empty-mini">
          <EmptyState description="你还没有在售物品" />
          <el-button type="primary" link @click="$router.push('/products/create')">去发布</el-button>
        </div>
        <ExchangeItemCard
          v-for="p in myProducts"
          :key="'o' + p.id"
          :item="toItem(p, 'offer')"
          selectable
          show-side
          :selected="form.offer_product_ids.includes(p.id)"
          @toggle="toggleOffer"
        />
      </el-col>

      <!-- 对方物品 -->
      <el-col :span="12">
        <h3 class="col-title">想换对方的物品（至少 1 件）</h3>
        <div v-if="targetProducts.length === 0" class="empty-mini">
          <EmptyState description="未找到对方在售物品" />
        </div>
        <ExchangeItemCard
          v-for="p in targetProducts"
          :key="'t' + p.id"
          :item="toItem(p, 'target')"
          selectable
          show-side
          :selected="form.target_product_ids.includes(p.id)"
          @toggle="toggleTarget"
        />
      </el-col>
    </el-row>

    <el-divider />

    <el-form label-width="110px" class="form">
      <el-form-item label="补差价金额">
        <el-input-number v-model="form.top_up_amount" :min="0" :precision="2" :step="50" />
        <span class="hint">由下方选择的一方支付给另一方</span>
      </el-form-item>
      <el-form-item label="差价支付方">
        <el-radio-group v-model="form.top_up_payer">
          <el-radio value="offeror">我补给对方</el-radio>
          <el-radio value="offeree">对方补给我</el-radio>
        </el-radio-group>
      </el-form-item>
      <el-form-item label="有效期">
        <el-select v-model="form.ttl_hours" style="width: 180px">
          <el-option label="24 小时" :value="24" />
          <el-option label="3 天" :value="72" />
          <el-option label="7 天" :value="168" />
          <el-option label="30 天" :value="720" />
        </el-select>
        <span class="hint">到期未处理自动失效并释放物品</span>
      </el-form-item>
      <el-form-item label="说明">
        <el-input v-model="form.note" type="textarea" :rows="3" maxlength="500" show-word-limit placeholder="介绍物品成色、期望的交换方式等" />
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="submitting" @click="submit">发起提案</el-button>
        <el-button @click="$router.back()">取消</el-button>
      </el-form-item>
    </el-form>
  </el-card>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import * as productApi from '../../api/product'
import { useExchangeStore } from '../../stores/exchangeStore'
import ExchangeItemCard from '../../components/ExchangeItemCard.vue'
import EmptyState from '../../components/EmptyState.vue'
import type { ExchangeItemVO, ProductVO } from '../../api/types'

const route = useRoute()
const router = useRouter()
const exchangeStore = useExchangeStore()

const loading = ref(false)
const submitting = ref(false)
const myProducts = ref<ProductVO[]>([])
const targetProducts = ref<ProductVO[]>([])

const form = reactive({
  offer_product_ids: [] as number[],
  target_product_ids: [] as number[],
  note: '',
  top_up_amount: 0,
  top_up_payer: 'offeror' as 'offeror' | 'offeree',
  ttl_hours: 72
})

onMounted(load)

async function load() {
  loading.value = true
  try {
    const targetId = Number(route.query.target_product_id)
    if (!targetId) {
      ElMessage.error('缺少目标物品')
      router.back()
      return
    }
    const targetRes: any = await productApi.getProduct(targetId)
    const target = targetRes.data as ProductVO
    if (target.status !== 'on_sale') {
      ElMessage.warning('对方物品已非在售，无法换物')
    }
    // 对方全部在售物品，供多选。
    const sellerRes: any = await productApi.listProducts({ seller_id: target.seller_id, status: 'on_sale', page: 1, page_size: 50 })
    targetProducts.value = (sellerRes.data.list || []) as ProductVO[]
    if (!targetProducts.value.find((p) => p.id === targetId) && target.status === 'on_sale') {
      targetProducts.value = [target, ...targetProducts.value]
    }
    form.target_product_ids = target.status === 'on_sale' ? [targetId] : []

    // 我的在售物品。
    const mineRes: any = await productApi.myProducts({ page: 1, page_size: 50 })
    const list = (mineRes.data.list || []) as ProductVO[]
    myProducts.value = list.filter((p) => p.status === 'on_sale')
  } finally {
    loading.value = false
  }
}

function toItem(p: ProductVO, side: 'offer' | 'target'): ExchangeItemVO {
  return {
    id: 0,
    product_id: p.id,
    owner_id: p.seller_id,
    side,
    title: p.title,
    price: p.price,
    image: p.images?.[0] || '',
    product: p
  }
}

function toggleOffer(id: number) {
  toggleIn(form.offer_product_ids, id)
}

function toggleTarget(id: number) {
  toggleIn(form.target_product_ids, id)
}

function toggleIn(arr: number[], id: number) {
  const idx = arr.indexOf(id)
  if (idx >= 0) arr.splice(idx, 1)
  else arr.push(id)
}

async function submit() {
  if (form.offer_product_ids.length === 0) {
    ElMessage.warning('请至少选择一件自己换出的物品')
    return
  }
  if (form.target_product_ids.length === 0) {
    ElMessage.warning('请至少选择一件对方物品')
    return
  }
  submitting.value = true
  try {
    const data = await exchangeStore.create({
      offer_product_ids: form.offer_product_ids,
      target_product_ids: form.target_product_ids,
      note: form.note,
      top_up_amount: form.top_up_amount,
      top_up_payer: form.top_up_payer,
      ttl_hours: form.ttl_hours
    })
    ElMessage.success('换物提案已发起')
    router.replace(`/exchange/${data.id}`)
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.col-title {
  margin: 0 0 12px;
  font-size: 15px;
  color: #303133;
}
.empty-mini {
  text-align: center;
  padding: 12px 0;
}
.form {
  max-width: 640px;
}
.hint {
  margin-left: 12px;
  color: #909399;
  font-size: 12px;
}
</style>
