<template>
  <div>
    <div class="page-header">
      <h3>用户管理</h3>
      <el-button type="primary" @click="showCreateDialog">
        <el-icon>
          <Plus />
        </el-icon> 新建用户
      </el-button>
    </div>

    <el-table :data="users" stripe>
      <el-table-column prop="id" label="ID" width="80" />
      <el-table-column prop="username" label="用户名" width="150" />
      <el-table-column prop="display_name" label="显示名称" width="150" />
      <el-table-column prop="role" label="角色" width="100">
        <template #default="scope">
          <el-tag :type="scope.row.role === 'admin' ? 'danger' : 'primary'" size="small">
            {{ scope.row.role === 'admin' ? '管理员' : '普通用户' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="创建时间" width="180">
        <template #default="scope">
          {{ formatDate(scope.row.created_at) }}
        </template>
      </el-table-column>
      <el-table-column label="操作" width="250">
        <template #default="scope">
          <el-button size="small" @click="editUser(scope.row)">编辑</el-button>
          <el-button size="small" type="warning" @click="resetPassword(scope.row)">重置密码</el-button>
          <el-button size="small" type="danger" @click="deleteUser(scope.row)"
            :disabled="scope.row.role === 'admin'">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="pagination-wrapper">
      <el-pagination v-model:current-page="currentPage" v-model:page-size="pageSize" :total="total"
        :page-sizes="[10, 20, 50, 100]" layout="total, sizes, prev, pager, next" @size-change="fetchUsers"
        @current-change="fetchUsers" />
    </div>

    <!-- 创建/编辑对话框 -->
    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑用户' : '新建用户'" width="480px">
      <el-form ref="formRef" :model="form" :rules="formRules" label-width="80px">
        <el-form-item label="用户名" prop="username" v-if="!isEdit">
          <el-input v-model="form.username" />
        </el-form-item>
        <el-form-item label="密码" prop="password" v-if="!isEdit">
          <el-input v-model="form.password" type="password" show-password />
        </el-form-item>
        <el-form-item label="显示名称">
          <el-input v-model="form.display_name" />
        </el-form-item>
        <el-form-item label="角色" prop="role">
          <el-select v-model="form.role">
            <el-option label="管理员" value="admin" />
            <el-option label="普通用户" value="user" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitForm">确认</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码对话框 -->
    <el-dialog v-model="resetDialogVisible" title="重置密码" width="400px">
      <el-form :model="resetForm" label-width="80px">
        <el-form-item label="新密码">
          <el-input v-model="resetForm.password" type="password" show-password placeholder="至少6位" />
        </el-form-item>
        <el-form-item label="确认密码">
          <el-input v-model="resetForm.confirm_password" type="password" show-password placeholder="再次输入新密码" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="resetDialogVisible = false">取消</el-button>
        <el-button type="primary" @click="submitResetPassword">确认重置</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { adminUserApi } from '../../api'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { FormInstance } from 'element-plus'

const users = ref<any[]>([])
const currentPage = ref(1)
const pageSize = ref(20)
const total = ref(0)
const dialogVisible = ref(false)
const resetDialogVisible = ref(false)
const isEdit = ref(false)
const editId = ref(0)
const formRef = ref<FormInstance>()

const form = ref({
  username: '',
  password: '',
  display_name: '',
  role: 'user',
})

const formRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
  role: [{ required: true, message: '请选择角色', trigger: 'change' }],
}

const resetForm = ref({
  password: '',
  confirm_password: '',
})

function formatDate(d: string): string {
  if (!d) return ''
  return new Date(d).toLocaleString('zh-CN')
}

async function fetchUsers() {
  try {
    const res = await adminUserApi.list({ page: currentPage.value, page_size: pageSize.value })
    users.value = res.data.items || []
    total.value = res.data.total || 0
  } catch (e) {
    console.error(e)
  }
}

function showCreateDialog() {
  isEdit.value = false
  form.value = { username: '', password: '', display_name: '', role: 'user' }
  dialogVisible.value = true
}

function editUser(user: any) {
  isEdit.value = true
  editId.value = user.id
  form.value = {
    username: user.username,
    password: '',
    display_name: user.display_name || '',
    role: user.role,
  }
  dialogVisible.value = true
}

async function submitForm() {
  if (!formRef.value) return
  await formRef.value.validate(async (valid) => {
    if (!valid) return
    try {
      if (isEdit.value) {
        const data: any = {
          display_name: form.value.display_name,
          role: form.value.role,
        }
        await adminUserApi.update(editId.value, data)
        ElMessage.success('更新成功')
      } else {
        await adminUserApi.create(form.value)
        ElMessage.success('创建成功')
      }
      dialogVisible.value = false
      fetchUsers()
    } catch (e) {
      // 错误已在拦截器中处理
    }
  })
}

function resetPassword(user: any) {
  editId.value = user.id
  resetForm.value = { password: '', confirm_password: '' }
  resetDialogVisible.value = true
}

async function submitResetPassword() {
  if (!resetForm.value.password) {
    ElMessage.warning('请输入新密码')
    return
  }
  if (resetForm.value.password.length < 6) {
    ElMessage.warning('密码长度不能少于6位')
    return
  }
  if (resetForm.value.password !== resetForm.value.confirm_password) {
    ElMessage.warning('两次输入的密码不一致')
    return
  }
  try {
    await adminUserApi.update(editId.value, { password: resetForm.value.password })
    ElMessage.success('密码重置成功')
    resetDialogVisible.value = false
  } catch (e) {
    // 错误已处理
  }
}

async function deleteUser(user: any) {
  try {
    await ElMessageBox.confirm(`确定删除用户 "${user.username}" 吗？`, '确认删除', {
      type: 'warning',
    })
    await adminUserApi.delete(user.id)
    ElMessage.success('删除成功')
    fetchUsers()
  } catch (e) {
    // 取消或不处理
  }
}

onMounted(() => {
  fetchUsers()
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

.pagination-wrapper {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
