import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { authApi } from '../api'

export const useUserStore = defineStore('user', () => {
  const token = ref(localStorage.getItem('token') || '')
  const username = ref(localStorage.getItem('username') || '')
  const role = ref(localStorage.getItem('role') || '')
  const displayName = ref(localStorage.getItem('displayName') || '')
  const userId = ref(Number(localStorage.getItem('userId')) || 0)

  const isLoggedIn = computed(() => !!token.value)
  const isAdmin = computed(() => role.value === 'admin')

  async function login(user: string, password: string) {
    const res = await authApi.login({ username: user, password: password })
    const data = res.data
    token.value = data.token
    username.value = data.user.username
    role.value = data.user.role
    displayName.value = data.user.display_name || ''
    userId.value = data.user.id

    localStorage.setItem('token', data.token)
    localStorage.setItem('username', data.user.username)
    localStorage.setItem('role', data.user.role)
    localStorage.setItem('displayName', data.user.display_name || '')
    localStorage.setItem('userId', String(data.user.id))
  }

  function logout() {
    token.value = ''
    username.value = ''
    role.value = ''
    displayName.value = ''
    userId.value = 0
    localStorage.removeItem('token')
    localStorage.removeItem('username')
    localStorage.removeItem('role')
    localStorage.removeItem('displayName')
    localStorage.removeItem('userId')
  }

  return { token, username, role, displayName, userId, isLoggedIn, isAdmin, login, logout }
})
