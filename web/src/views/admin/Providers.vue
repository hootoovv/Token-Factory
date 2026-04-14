<template>
  <div>
    <div class="page-header">
      <h3>供应商管理</h3>
      <el-button type="primary" @click="showCreateDialog">
        <el-icon><Plus /></el-icon> 新建供应商
      </el-button>
    </div>

    <el-table :data="providers" stripe>
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" width="130" />
      <el-table-column prop="description" label="描述" min-width="150" show-overflow-tooltip />
      <el-table-column prop="base_url" label="Base URL" min-width="200" show-overflow-tooltip />
      <el-table-column prop="timeout" label="超时(s)" width="80" />
      <el-table-column prop="retry" label="重试" width="70" />
      <el-table-column prop="status" label="状态" width="100">
        <template #default="scope">
          <el-tag :type="getStatusType(scope.row.status)" size="small" effect="dark">
            {{ getStatusText(scope.row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="200">
        <template #default="scope">
          <el-button size="small" @click="editProvider(scope.row)">编辑</el-button>
          <el-button size="small" type="danger" @click="deleteProvider(scope.row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑供应商' : '新建供应商'" width="560px">
      <el-form ref="formRef" :model="form" :rules="formRules" label-width="100px">
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="如: OpenAI, Anthropic" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </el-form-item>
        <el-form-item label="Base URL" prop="base_url">
          <el-input v-model="form.base_url" placeholder="如: https://api.openai.com" />
        </el-form-item>
        <el-form-item label="API Key" prop="api_key">
          <el-input v-model="form.api_key" type="password" show-password />
        </el-form-item>
        <el-form-item label="超时(秒)">
          <el-input-number v-model="form.timeout" :min="5" :max="300" />
        </el-form-item>
        <el-form-item label="重试次数">
          <el-input-number v-model="form.retry" :min="0" :max="10" />
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="form.status">
            <el-option label="工作中" value="active" />
            <el-option label="冷却中" value="cooldown" />
            <el-option label="欠费" value="arrears" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitForm">确认</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminProviderApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance } from 'element-plus'

const providers = ref<any[]>([])
const dialogVisible = ref(false)
const isEdit = ref(false)
const editId = ref(0)
const formRef = ref<FormInstance>()

const form = ref({
  name: '',
  description: '',
  base_url: '',
  api_key: '',
  timeout: 60,
  retry: 1,
  status: 'active',
})

const formRules = {
  name: [{ required: true, message: '请输入名称', trigger: 'blur' }],
  base_url: [{ required: true, message: '请输入Base URL', trigger: 'blur' }],
  api_key: [{ required: true, message: '请输入API Key', trigger: 'blur' }],
}

function getStatusType(status: string): string {
  switch (status) {
    case 'active': return 'success'
    case 'cooldown': return 'warning'
    case 'arrears': return 'danger'
    default: return 'info'
  }
}

function getStatusText(status: string): string {
  switch (status) {
    case 'active': return '工作中'
    case 'cooldown': return '冷却中'
    case 'arrears': return '欠费'
    default: return status
  }
}

async function fetchProviders() {
  try {
    const res = await adminProviderApi.list()
    providers.value = res.data || []
  } catch (e) {
    console.error(e)
  }
}

function showCreateDialog() {
  isEdit.value = false
  form.value = { name: '', description: '', base_url: '', api_key: '', timeout: 30, retry: 3, status: 'active' }
  dialogVisible.value = true
}

function editProvider(provider: any) {
  isEdit.value = true
  editId.value = provider.id
  form.value = {
    name: provider.name,
    description: provider.description || '',
    base_url: provider.base_url,
    api_key: provider.api_key || '',
    timeout: provider.timeout || 60,
    retry: provider.retry || 1,
    status: provider.status || 'active',
  }
  dialogVisible.value = true
}

async function submitForm() {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    try {
      if (isEdit.value) {
        await adminProviderApi.update(editId.value, form.value)
        ElMessage.success('更新成功')
      } else {
        await adminProviderApi.create(form.value)
        ElMessage.success('创建成功')
      }
      dialogVisible.value = false
      fetchProviders()
    } catch (e) {
      // 错误已处理
    }
  })
}

async function deleteProvider(provider: any) {
  try {
    await ElMessageBox.confirm(`确定删除供应商 "${provider.name}" 吗？相关的模型映射也将被删除。`, '确认删除', {
      type: 'warning',
    })
    await adminProviderApi.delete(provider.id)
    ElMessage.success('删除成功')
    fetchProviders()
  } catch (e) {
    // 取消
  }
}

onMounted(() => {
  fetchProviders()
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
