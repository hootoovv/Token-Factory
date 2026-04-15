<template>
  <div class="user-layout">
    <el-container>
      <el-aside width="200px">
        <el-menu :default-active="activeMenu" router class="user-menu">
          <div class="menu-header">
            <h3>个人中心</h3>
          </div>
          <el-menu-item index="/user/api-keys">
            <el-icon>
              <Key />
            </el-icon>
            <span>API密钥</span>
          </el-menu-item>
          <el-menu-item index="/user/usage">
            <el-icon>
              <List />
            </el-icon>
            <span>使用记录</span>
          </el-menu-item>
          <el-menu-item index="/user/stats">
            <el-icon>
              <DataAnalysis />
            </el-icon>
            <span>使用统计</span>
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

const route = useRoute()
const activeMenu = computed(() => route.path)
</script>

<style scoped>
.user-layout {
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

.user-menu {
  border-right: none;
}

.el-main {
  padding: 20px;
  min-height: 500px;
}
</style>
