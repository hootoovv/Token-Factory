<template>
  <div>
    <div class="page-header">
      <h3>模型管理</h3>
      <div>
        <el-button type="primary" @click="showCreateModelDialog">
          <el-icon><Plus /></el-icon> 新建模型
        </el-button>
        <el-button @click="showMappingDialog = true">
          <el-icon><Link /></el-icon> 管理映射
        </el-button>
      </div>
    </div>

    <!-- 模型列表 -->
    <el-card shadow="never" style="margin-bottom: 20px;">
      <template #header><span>模型列表</span></template>
      <el-table :data="models" stripe>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="name" label="模型名称" width="200" />
        <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip />
        <el-table-column label="已映射供应商" min-width="250">
          <template #default="scope">
            <el-tag v-for="mp in getModelProviders(scope.row.id)" :key="mp.id" size="small" style="margin-right: 6px;">
              {{ getProviderName(mp.provider_id) }} ({{ mp.provider_model_name }})
            </el-tag>
            <span v-if="getModelProviders(scope.row.id).length === 0" style="color: #909399;">未映射</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150">
          <template #default="scope">
            <el-button size="small" @click="editModel(scope.row)">编辑</el-button>
            <el-button size="small" type="danger" @click="deleteModel(scope.row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

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

    <!-- 映射管理对话框 -->
    <el-dialog v-model="showMappingDialog" title="模型-供应商映射管理" width="700px">
      <div style="margin-bottom: 16px;">
        <el-button type="primary" size="small" @click="showCreateMappingDialog">
          <el-icon><Plus /></el-icon> 添加映射
        </el-button>
      </div>
      <el-table :data="mappings" stripe size="small">
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column label="模型" width="150">
          <template #default="scope">
            {{ getModelName(scope.row.model_id) }}
          </template>
        </el-table-column>
        <el-table-column label="供应商" width="130">
          <template #default="scope">
            {{ getProviderName(scope.row.provider_id) }}
          </template>
        </el-table-column>
        <el-table-column prop="provider_model_name" label="供应商侧模型名" width="200" />
        <el-table-column label="操作" width="80">
          <template #default="scope">
            <el-button size="small" type="danger" text @click="deleteMapping(scope.row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <!-- 创建映射 -->
      <el-dialog v-model="mappingCreateVisible" title="添加映射" width="450px" append-to-body>
        <el-form ref="mappingFormRef" :model="mappingForm" :rules="mappingRules" label-width="120px">
          <el-form-item label="模型" prop="model_id">
            <el-select v-model="mappingForm.model_id" placeholder="选择模型">
              <el-option v-for="m in models" :key="m.id" :label="m.name" :value="m.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="供应商" prop="provider_id">
            <el-select v-model="mappingForm.provider_id" placeholder="选择供应商">
              <el-option v-for="p in providers" :key="p.id" :label="p.name" :value="p.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="供应商侧模型名" prop="provider_model_name">
            <el-input v-model="mappingForm.provider_model_name" placeholder="如: gpt-4-0613" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="mappingCreateVisible = false">取消</el-button>
          <el-button type="primary" @click="submitMappingForm">确认</el-button>
        </template>
      </el-dialog>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminModelApi, adminProviderApi, adminModelProviderApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance } from 'element-plus'

const models = ref<any[]>([])
const providers = ref<any[]>([])
const mappings = ref<any[]>([])

const modelDialogVisible = ref(false)
const isEditModel = ref(false)
const editModelId = ref(0)
const modelFormRef = ref<FormInstance>()
const modelForm = ref({ name: '', description: '' })
const modelRules = {
  name: [{ required: true, message: '请输入模型名称', trigger: 'blur' }],
}

const showMappingDialog = ref(false)
const mappingCreateVisible = ref(false)
const mappingFormRef = ref<FormInstance>()
const mappingForm = ref({ model_id: 0, provider_id: 0, provider_model_name: '' })
const mappingRules = {
  model_id: [{ required: true, message: '请选择模型', trigger: 'change' }],
  provider_id: [{ required: true, message: '请选择供应商', trigger: 'change' }],
  provider_model_name: [{ required: true, message: '请输入供应商侧模型名', trigger: 'blur' }],
}

function getModelName(id: number): string {
  return models.value.find(m => m.id === id)?.name || `#${id}`
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
  } catch (e) {
    console.error(e)
  }
}

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
    await ElMessageBox.confirm(`确定删除模型 "${model.name}" 吗？相关映射也将被删除。`, '确认', { type: 'warning' })
    await adminModelApi.delete(model.id)
    ElMessage.success('删除成功')
    fetchData()
  } catch (e) { /* 取消 */ }
}

function showCreateMappingDialog() {
  mappingForm.value = { model_id: 0, provider_id: 0, provider_model_name: '' }
  mappingCreateVisible.value = true
}

async function submitMappingForm() {
  if (!mappingFormRef.value) return
  await mappingFormRef.value.validate(async (valid) => {
    if (!valid) return
    try {
      await adminModelProviderApi.create(mappingForm.value)
      ElMessage.success('映射添加成功')
      mappingCreateVisible.value = false
      fetchData()
    } catch (e) { /* 已处理 */ }
  })
}

async function deleteMapping(mapping: any) {
  try {
    await adminModelProviderApi.delete(mapping.id)
    ElMessage.success('映射删除成功')
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
</style>
