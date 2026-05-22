<template>
  <div>
    <div class="page-header">
      <h3>使用统计</h3>
      <div class="filter-bar">
        <el-radio-group v-model="timeFilter" size="small" @change="fetchData">
          <el-radio-button label="1h">1小时</el-radio-button>
          <el-radio-button label="today">今天</el-radio-button>
          <el-radio-button label="week">本周</el-radio-button>
          <el-radio-button label="month">本月</el-radio-button>
        </el-radio-group>
      </div>
    </div>

    <el-row :gutter="16" class="stats-row">
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <el-statistic title="总调用次数" :value="stats.total_calls || 0" />
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <el-statistic title="输入数据量" :value="formatBytes(stats.total_input_bytes || 0)" />
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <el-statistic title="输出数据量" :value="formatBytes(stats.total_output_bytes || 0)" />
        </el-card>
      </el-col>
      <el-col :span="6">
        <el-card shadow="hover" class="stat-card">
          <el-statistic title="平均耗时(ms)" :value="Number(stats.avg_duration || 0).toFixed(0)" />
        </el-card>
      </el-col>
    </el-row>

    <!-- 修改密码 -->
    <el-card shadow="never" style="margin-top: 20px;">
      <template #header><span>修改密码</span></template>
      <el-form :model="passwordForm" label-width="100px" style="max-width: 400px;">
        <el-form-item label="旧密码">
          <el-input v-model="passwordForm.old_password" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="passwordForm.new_password" type="password" show-password placeholder="至少6位" />
        </el-form-item>
        <el-form-item label="确认新密码">
          <el-input v-model="passwordForm.confirm_password" type="password" show-password placeholder="再次输入新密码" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="changePassword">修改密码</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { userApi } from '../../api'
import { ElMessage } from 'element-plus'

const timeFilter = ref('1h')
const stats = ref<any>({})

const passwordForm = ref({
  old_password: '',
  new_password: '',
  confirm_password: '',
})

function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return size.toFixed(1) + ' ' + units[i]
}

async function fetchData() {
  try {
    const res = await userApi.stats(timeFilter.value)
    stats.value = res.data || {}
  } catch (e) {
    console.error(e)
  }
}

async function changePassword() {
  if (!passwordForm.value.old_password || !passwordForm.value.new_password) {
    ElMessage.warning('请填写完整')
    return
  }
  if (passwordForm.value.new_password.length < 6) {
    ElMessage.warning('新密码长度不能少于6位')
    return
  }
  if (passwordForm.value.new_password !== passwordForm.value.confirm_password) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  try {
    await userApi.changePassword(passwordForm.value)
    ElMessage.success('密码修改成功')
    passwordForm.value = { old_password: '', new_password: '', confirm_password: '' }
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

.filter-bar {
  display: flex;
  align-items: center;
}

.stats-row {
  margin-bottom: 16px;
}

.stat-card {
  text-align: center;
}
</style>
