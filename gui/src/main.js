import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import HomeView from './views/HomeView.vue'
import RulesView from './views/RulesView.vue'
import SettingsView from './views/SettingsView.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: HomeView },
    { path: '/rules', component: RulesView },
    { path: '/settings', component: SettingsView },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ]
})

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
