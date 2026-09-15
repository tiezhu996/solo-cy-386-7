<template>
  <!-- 换物物品卡片：发起页选择态与详情页快照态共用同一组件 -->
  <div class="ex-item" :class="{ selectable: selectable, selected: selected }" @click="emit('toggle', item.product_id)">
    <el-image v-if="cover" :src="cover" fit="cover" class="thumb" />
    <div v-else class="thumb no-img">无图</div>
    <div class="meta">
      <div class="title">{{ item.title }}</div>
      <div class="price">¥{{ formatPrice(item.price) }}</div>
      <div v-if="sideText" class="side">
        <el-tag size="small" :type="item.side === 'offer' ? 'warning' : 'success'">{{ sideText }}</el-tag>
      </div>
    </div>
    <div v-if="selectable" class="check">
      <el-checkbox :model-value="selected" @click.stop @change="emit('toggle', item.product_id)" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatPrice } from '../utils/format'
import type { ExchangeItemVO } from '../api/types'
import { ExchangeSideText } from '../constants'

const props = defineProps<{
  item: ExchangeItemVO
  selectable?: boolean
  selected?: boolean
  showSide?: boolean
}>()

const emit = defineEmits<{ (e: 'toggle', productId: number): void }>()

const cover = computed(() => {
  if (props.item.image) return props.item.image
  return props.item.product?.images?.[0] || ''
})

const sideText = computed(() => (props.showSide ? ExchangeSideText[props.item.side] ?? props.item.side : ''))
</script>

<style scoped>
.ex-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px;
  border: 1px solid #ebeef5;
  border-radius: 8px;
  margin-bottom: 8px;
  background: #fff;
}
.ex-item.selectable {
  cursor: pointer;
}
.ex-item.selectable:hover {
  border-color: #409eff;
}
.ex-item.selected {
  border-color: #409eff;
  background: #ecf5ff;
}
.thumb {
  width: 56px;
  height: 56px;
  border-radius: 6px;
  flex-shrink: 0;
}
.no-img {
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f5f7fa;
  color: #909399;
  font-size: 12px;
}
.meta {
  flex: 1;
  min-width: 0;
}
.title {
  font-weight: 600;
  color: #303133;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.price {
  color: #f56c6c;
  font-size: 14px;
  margin-top: 2px;
}
.side {
  margin-top: 4px;
}
.check {
  margin-right: 4px;
}
</style>
