<template>
  <router-view />
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useUserStore } from './stores/user'
import { authApi } from './api'

const userStore = useUserStore()

// 应用启动时：如果用户已登录（token存在）但 transmissionKey 为空（页面刷新导致内存丢失），
// 则从服务端重新获取 transmissionKey
onMounted(async () => {
  if (userStore.isLoggedIn && !userStore.transmissionKey) {
    try {
      const res = await authApi.getTransmissionKey()
      userStore.transmissionKey = res.data.transmission_key || ''
    } catch (e) {
      // 获取失败（如token已过期），静默处理，401拦截器会自动跳转登录页
      console.warn('获取传输密钥失败，可能需要重新登录', e)
    }
  }
})
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
  background-color: #f5f7fa;
}
</style>
