<template>
  <div>
    <div class="page-header">
      <h3>模型管理</h3>
      <el-button type="primary" @click="showCreateModelDialog">
        <el-icon><Plus /></el-icon> 新建模型
      </el-button>
    </div>

    <!-- 模型列表 - Collapse -->
    <el-collapse v-model="activeCollapse" class="model-collapse">
      <el-collapse-item
        v-for="model in models"
        :key="model.id"
        :name="model.id"
      >
        <!-- 自定义标题 -->
        <template #title>
          <div class="collapse-title">
            <div class="collapse-title-left">
              <span class="model-name">{{ model.name }}</span>
              <span v-if="model.description" class="model-desc">{{ model.description }}</span>
              <el-tag size="small" type="info" class="provider-count">
                {{ getModelProviders(model.id).length }} 个供应商
              </el-tag>
            </div>
            <div class="collapse-title-right" @click.stop>
              <el-button size="small" text @click="editModel(model)">
                <el-icon><Edit /></el-icon> 编辑
              </el-button>
              <el-button size="small" text type="danger" @click="deleteModel(model)">
                <el-icon><Delete /></el-icon> 删除
              </el-button>
              <el-button size="small" text type="primary" @click="addProviderRow(model)">
                <el-icon><Plus /></el-icon> 添加供应商
              </el-button>
            </div>
          </div>
        </template>

        <!-- 展开内容：关联供应商列表 -->
        <div class="provider-list">
          <div v-if="getModelProviders(model.id).length === 0 && !hasNewRow(model.id)" class="empty-tip">
            暂无关联供应商，点击"添加供应商"按钮添加
          </div>
          <div
            v-for="mp in getModelProviders(model.id)"
            :key="mp.id"
            class="provider-row"
          >
            <el-select
              :model-value="mp.provider_id"
              placeholder="选择供应商"
              class="provider-select"
              @change="(val: number) => updateMappingProvider(mp, val)"
            >
              <el-option
                v-for="p in providers"
                :key="p.id"
                :label="p.name"
                :value="p.id"
              />
            </el-select>
            <el-input
              :model-value="mp.provider_model_name"
              placeholder="供应商侧模型名称"
              class="model-name-input"
              @change="(val: string) => updateMappingModelName(mp, val)"
            />
            <el-button
              size="small"
              type="danger"
              text
              @click="deleteMapping(mp)"
            >
              <el-icon><Delete /></el-icon> 删除
            </el-button>
          </div>

          <!-- 新增供应商行 -->
          <div v-if="hasNewRow(model.id)" class="provider-row new-provider-row">
            <el-select
              v-model="newRowData[model.id].provider_id"
              placeholder="选择供应商"
              class="provider-select"
            >
              <el-option
                v-for="p in providers"
                :key="p.id"
                :label="p.name"
                :value="p.id"
              />
            </el-select>
            <el-input
              v-model="newRowData[model.id].provider_model_name"
              placeholder="供应商侧模型名称"
              class="model-name-input"
            />
            <el-button size="small" type="primary" @click="submitNewProvider(model.id)">
              确认
            </el-button>
            <el-button size="small" @click="cancelNewRow(model.id)">
              取消
            </el-button>
          </div>
        </div>
      </el-collapse-item>
    </el-collapse>

    <!-- 空状态 -->
    <el-empty v-if="models.length === 0" description="暂无模型，点击右上角新建模型" />

    <!-- 创建/编辑模型对话框 -->
    <el-dialog v-model="modelDialogVisible" :title="isEditModel ? '编辑模型' : '新建模型'" width="480px">
      <el-form ref="modelFormRef" :model="modelForm" :rules="modelRules" label-width="80px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="modelForm.name" placeholder="如: gpt-4, claude-3-opus" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="modelForm.description" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="modelDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitModelForm">确认</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { adminModelApi, adminProviderApi, adminModelProviderApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance } from 'element-plus'

const models = ref<any[]>([])
const providers = ref<any[]>([])
const mappings = ref<any[]>([])

// Collapse 展开状态
const activeCollapse = ref<number[]>([])

// 模型对话框
const modelDialogVisible = ref(false)
const isEditModel = ref(false)
const editModelId = ref(0)
const modelFormRef = ref<FormInstance>()
const modelForm = ref({ name: '', description: '' })
const modelRules = {
  name: [{ required: true, message: '请输入模型名称', trigger: 'blur' }],
}

// 新增供应商行状态：按 modelId 存储
const newRowData = reactive<Record<number, { provider_id: number; provider_model_name: string }>>({})

function hasNewRow(modelId: number): boolean {
  return modelId in newRowData
}

function getProviderName(id: number): string {
  return providers.value.find(p => p.id === id)?.name || `#${id}`
}

function getModelProviders(modelId: number) {
  return mappings.value.filter(m => m.model_id === modelId)
}

async function fetchData() {
  try {
    const [modelsRes, providersRes, mappingsRes] = await Promise.all([
      adminModelApi.list(),
      adminProviderApi.list(),
      adminModelProviderApi.list(),
    ])
    models.value = modelsRes.data || []
    providers.value = providersRes.data || []
    mappings.value = mappingsRes.data || []
    // 默认展开所有折叠面板
    activeCollapse.value = models.value.map((m: any) => m.id)
  } catch (e) {
    console.error(e)
  }
}

// ============ 模型 CRUD ============

function showCreateModelDialog() {
  isEditModel.value = false
  modelForm.value = { name: '', description: '' }
  modelDialogVisible.value = true
}

function editModel(model: any) {
  isEditModel.value = true
  editModelId.value = model.id
  modelForm.value = { name: model.name, description: model.description || '' }
  modelDialogVisible.value = true
}

async function submitModelForm() {
  if (!modelFormRef.value) return
  await modelFormRef.value.validate(async (valid) => {
    if (!valid) return
    try {
      if (isEditModel.value) {
        await adminModelApi.update(editModelId.value, modelForm.value)
        ElMessage.success('更新成功')
      } else {
        await adminModelApi.create(modelForm.value)
        ElMessage.success('创建成功')
      }
      modelDialogVisible.value = false
      fetchData()
    } catch (e) { /* 已处理 */ }
  })
}

async function deleteModel(model: any) {
  try {
    await ElMessageBox.confirm(
      `确定删除模型 "${model.name}" 吗？相关映射也将被删除。`,
      '确认',
      { type: 'warning' }
    )
    await adminModelApi.delete(model.id)
    ElMessage.success('删除成功')
    // 从展开列表中移除
    activeCollapse.value = activeCollapse.value.filter(id => id !== model.id)
    fetchData()
  } catch (e) { /* 取消 */ }
}

// ============ 供应商映射操作 ============

function addProviderRow(model: any) {
  // 如果未展开，自动展开
  if (!activeCollapse.value.includes(model.id)) {
    activeCollapse.value.push(model.id)
  }
  // 初始化新行数据，默认选中第一个供应商
  const defaultProviderId = providers.value.length > 0 ? providers.value[0].id : 0
  newRowData[model.id] = {
    provider_id: defaultProviderId,
    provider_model_name: '',
  }
}

function cancelNewRow(modelId: number) {
  delete newRowData[modelId]
}

async function submitNewProvider(modelId: number) {
  const row = newRowData[modelId]
  if (!row) return
  if (!row.provider_id) {
    ElMessage.warning('请选择供应商')
    return
  }
  if (!row.provider_model_name.trim()) {
    ElMessage.warning('请输入供应商侧模型名称')
    return
  }
  try {
    await adminModelProviderApi.create({
      model_id: modelId,
      provider_id: row.provider_id,
      provider_model_name: row.provider_model_name.trim(),
    })
    ElMessage.success('供应商添加成功')
    delete newRowData[modelId]
    fetchData()
  } catch (e) { /* 已处理 */ }
}

async function deleteMapping(mapping: any) {
  try {
    await ElMessageBox.confirm(
      `确定删除此供应商映射吗？`,
      '确认',
      { type: 'warning' }
    )
    await adminModelProviderApi.delete(mapping.id)
    ElMessage.success('映射删除成功')
    fetchData()
  } catch (e) { /* 取消 */ }
}

async function updateMappingProvider(mapping: any, newProviderId: number) {
  // 先删除旧的，再创建新的（因为后端可能不支持 update model-provider）
  try {
    await adminModelProviderApi.delete(mapping.id)
    await adminModelProviderApi.create({
      model_id: mapping.model_id,
      provider_id: newProviderId,
      provider_model_name: mapping.provider_model_name,
    })
    ElMessage.success('供应商更新成功')
    fetchData()
  } catch (e) { /* 已处理 */ }
}

async function updateMappingModelName(mapping: any, newName: string) {
  if (!newName.trim()) {
    ElMessage.warning('模型名称不能为空')
    return
  }
  try {
    await adminModelProviderApi.delete(mapping.id)
    await adminModelProviderApi.create({
      model_id: mapping.model_id,
      provider_id: mapping.provider_id,
      provider_model_name: newName.trim(),
    })
    ElMessage.success('模型名称更新成功')
    fetchData()
  } catch (e) { /* 已处理 */ }
}

onMounted(() => {
  fetchData()
})
</script>

<style scoped>
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 20px;
}
.page-header h3 {
  margin: 0;
}

.model-collapse {
  border: none;
}

.model-collapse :deep(.el-collapse-item__header) {
  height: auto;
  min-height: 48px;
  line-height: 1.5;
  padding: 8px 0;
  align-items: center;
}

.model-collapse :deep(.el-collapse-item__wrap) {
  border-bottom: none;
}

.model-collapse :deep(.el-collapse-item__content) {
  padding-bottom: 12px;
}

.collapse-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  padding-right: 12px;
  gap: 12px;
}

.collapse-title-left {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
}

.model-name {
  font-weight: 600;
  font-size: 24px;
  color: #303133;
  white-space: nowrap;
}

.model-desc {
  color: #909399;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 300px;
}

.provider-count {
  flex-shrink: 0;
}

.collapse-title-right {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.provider-list {
  padding: 4px 0;
}

.empty-tip {
  color: #909399;
  font-size: 13px;
  padding: 12px 0;
  text-align: center;
}

.provider-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  border-radius: 6px;
  transition: background-color 0.2s;
}

.provider-row:hover {
  background-color: #f5f7fa;
}

.provider-select {
  width: 200px;
  flex-shrink: 0;
}

.model-name-input {
  flex: 1;
  min-width: 200px;
}

.new-provider-row {
  margin-top: 8px;
  border-top: 1px dashed #dcdfe6;
  padding-top: 12px;
}
</style>