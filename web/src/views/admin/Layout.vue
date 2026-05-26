<template>
  <div class="admin-layout">
    <el-container>
      <el-aside width="220px">
        <el-menu :default-active="activeMenu" router class="admin-menu">
          <div class="menu-header">
            <h3>管理后台</h3>
          </div>
          <el-menu-item index="/admin/users">
            <el-icon>
              <User />
            </el-icon>
            <span>用户管理</span>
          </el-menu-item>
          <el-menu-item index="/admin/providers">
            <el-icon>
              <Connection />
            </el-icon>
            <span>供应商管理</span>
          </el-menu-item>
          <el-menu-item index="/admin/models">
            <el-icon>
              <Cpu />
            </el-icon>
            <span>模型管理</span>
          </el-menu-item>
          <el-menu-item index="/admin/stats">
            <el-icon>
              <DataAnalysis />
            </el-icon>
            <span>使用统计</span>
          </el-menu-item>
          <el-menu-item index="/admin/audit-logs">
            <el-icon>
              <Document />
            </el-icon>
            <span>审计日志</span>
          </el-menu-item>
          <el-menu-item index="/admin/call-records">
            <el-icon>
              <Phone />
            </el-icon>
            <span>调用记录</span>
          </el-menu-item>
          <el-divider />
          <el-menu-item index="/">
            <el-icon>
              <HomeFilled />
            </el-icon>
            <span>返回首页</span>
          </el-menu-item>
        </el-menu>
      </el-aside>
      <el-main>
        <router-view />
      </el-main>
    </el-container>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { Phone } from '@element-plus/icons-vue'

const route = useRoute()
const activeMenu = computed(() => route.path)
</script>

<style scoped>
.admin-layout {
  background: #fff;
  border-radius: 8px;
  overflow: hidden;
  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.04);
}

.el-aside {
  background: #fff;
  border-right: 1px solid #e8e8e8;
}

.menu-header {
  padding: 20px;
  border-bottom: 1px solid #e8e8e8;
}

.menu-header h3 {
  margin: 0;
  color: #303133;
  font-size: 16px;
}

.admin-menu {
  border-right: none;
}

.el-main {
  padding: 20px;
  min-height: 600px;
}
</style>
