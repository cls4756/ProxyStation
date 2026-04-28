<template>
  <div class="rules-page">
    <!-- 左侧：路由规则 -->
    <div class="rules-section">
      <div class="section-header">
        <span class="section-title">🔀 路由规则</span>
        <div class="section-actions">
          <span class="hint-text">规则按顺序匹配，优先级从上到下</span>
          <button class="action-btn" @click="exportRules" title="导出规则为 JSON 文件">⬇ 导出</button>
          <button class="action-btn" @click="triggerImportRules" title="从 JSON 文件导入规则">⬆ 导入</button>
          <input ref="rulesFileInput" type="file" accept=".json" style="display:none" @change="importRules" />
          <button class="action-btn action-btn-primary" @click="openAddRule">＋ 添加规则</button>
        </div>
      </div>

      <!-- 数据文件状态 -->
      <div class="data-files-bar">
        <span class="data-label">数据文件：</span>
        <span v-for="f in dataFiles" :key="f.type" :class="['data-badge', f.exists ? 'data-ok' : 'data-missing']">
          {{ f.label }}
          <span v-if="f.exists">✓</span>
          <span v-else>✗</span>
        </span>
        <button class="action-btn" style="margin-left:auto" @click="showDataModal = true">⬇ 下载/更新数据文件</button>
      </div>

      <!-- 规则列表 -->
      <div class="rules-list">
        <div v-if="!rules.length" class="empty-hint">
          <p>暂无规则，所有流量将走默认出站（proxy）</p>
        </div>
        <div v-for="(rule, i) in rules" :key="rule.id" :class="['rule-row', !rule.enabled ? 'rule-disabled' : '']">
          <div class="rule-drag-handle" title="拖拽排序">⠿</div>
          <div class="rule-main">
            <div class="rule-top">
              <span class="rule-name">{{ rule.name || '未命名规则' }}</span>
              <span :class="['rule-action-badge', `action-${rule.action}`]">
                {{ actionLabel(rule) }}
              </span>
            </div>
            <div class="rule-conditions">
              <span v-if="rule.inboundTags?.length" class="cond-tag cond-inbound">
                入站: {{ rule.inboundTags.join(', ') }}
              </span>
              <span v-if="rule.domains?.length" class="cond-tag cond-domain">
                域名: {{ rule.domains.slice(0,3).join(', ') }}{{ rule.domains.length > 3 ? ` +${rule.domains.length-3}` : '' }}
              </span>
              <span v-if="rule.ips?.length" class="cond-tag cond-ip">
                IP: {{ rule.ips.slice(0,3).join(', ') }}{{ rule.ips.length > 3 ? ` +${rule.ips.length-3}` : '' }}
              </span>
              <span v-if="rule.ports" class="cond-tag cond-port">端口: {{ rule.ports }}</span>
              <span v-if="rule.protocol" class="cond-tag cond-proto">协议: {{ rule.protocol }}</span>
            </div>
          </div>
          <div class="rule-ops">
            <label class="toggle-switch" title="启用/禁用">
              <input type="checkbox" :checked="rule.enabled" @change="toggleRule(i)" />
              <span class="toggle-slider"></span>
            </label>
            <button class="icon-btn" @click="moveRule(i, -1)" :disabled="i === 0" title="上移">↑</button>
            <button class="icon-btn" @click="moveRule(i, 1)" :disabled="i === rules.length-1" title="下移">↓</button>
            <button class="icon-btn" @click="editRule(i)" title="编辑">✏</button>
            <button class="icon-btn icon-btn-danger" @click="deleteRule(i)" title="删除">✕</button>
          </div>
        </div>
      </div>
    </div>

    <!-- 右侧：入站 -->
    <div class="inbounds-section">
      <div class="section-header">
        <span class="section-title">📥 入站</span>
        <button class="action-btn action-btn-primary" @click="openAddInbound">＋ 添加入站</button>
      </div>
      <div class="inbounds-list">
        <!-- 内置入站（不可删除） -->
        <div class="inbound-row inbound-builtin">
          <div class="inbound-info">
            <span class="inbound-name">默认 SOCKS5</span>
            <span class="inbound-proto-badge">socks</span>
            <span class="inbound-addr">127.0.0.1:{{ socksPort }}</span>
            <span class="inbound-tag-badge">tag: socks-in</span>
            <span v-if="builtinSocks.username" class="inbound-auth-badge">🔒 认证</span>
          </div>
          <div class="rule-ops">
            <button class="icon-btn" @click="editBuiltinInbound('socks')" title="编辑">✏</button>
          </div>
        </div>
        <div class="inbound-row inbound-builtin">
          <div class="inbound-info">
            <span class="inbound-name">默认 HTTP</span>
            <span class="inbound-proto-badge">http</span>
            <span class="inbound-addr">127.0.0.1:{{ httpPort }}</span>
            <span class="inbound-tag-badge">tag: http-in</span>
            <span v-if="builtinHttp.username" class="inbound-auth-badge">🔒 认证</span>
          </div>
          <div class="rule-ops">
            <button class="icon-btn" @click="editBuiltinInbound('http')" title="编辑">✏</button>
          </div>
        </div>
        <!-- 自定义入站 -->
        <div v-for="(ib, i) in inbounds" :key="ib.id" class="inbound-row">
          <div class="inbound-info">
            <span class="inbound-name">{{ ib.name || ib.tag }}</span>
            <span class="inbound-proto-badge">{{ ib.protocol }}</span>
            <span class="inbound-addr">{{ ib.listen || '127.0.0.1' }}:{{ ib.port }}</span>
            <span class="inbound-tag-badge">tag: {{ ib.tag }}</span>
            <span v-if="ib.username" class="inbound-auth-badge">🔒 认证</span>
          </div>
          <div class="rule-ops">
            <button class="icon-btn" @click="editInbound(i)" title="编辑">✏</button>
            <button class="icon-btn icon-btn-danger" @click="deleteInbound(i)" title="删除">✕</button>
          </div>
        </div>
        <div v-if="!inbounds.length" class="empty-hint" style="padding:12px 16px;font-size:12px;color:#bfbfbf;border-top:1px solid #f5f5f5">
          暂无自定义入站
        </div>
      </div>
    </div>

    <!-- 编辑内置入站弹窗 -->
    <div class="modal-overlay" v-if="showBuiltinModal" @click.self="showBuiltinModal = false">
      <div class="modal-box" style="width:440px">
        <div class="modal-header">
          <span class="modal-title">编辑{{ builtinEditType === 'socks' ? ' SOCKS5' : ' HTTP' }} 入站</span>
          <span class="modal-close" @click="showBuiltinModal = false">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">监听端口</label>
            <input class="input" type="number" v-model.number="builtinForm.port" />
          </div>
          <div class="form-row">
            <div class="form-group" style="flex:1">
              <label class="form-label">用户名（可选）</label>
              <input class="input" v-model="builtinForm.username" placeholder="留空表示不需要认证" />
            </div>
            <div class="form-group" style="flex:1">
              <label class="form-label">密码（可选）</label>
              <input class="input" type="password" v-model="builtinForm.password" placeholder="留空表示不需要认证" />
            </div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showBuiltinModal = false">取消</button>
          <button class="btn btn-primary" @click="saveBuiltinInbound">保存</button>
        </div>
      </div>
    </div>

    <!-- 添加/编辑规则弹窗 -->
    <div class="modal-overlay" v-if="showRuleModal" @click.self="showRuleModal = false">
      <div class="modal-box" style="width:600px">
        <div class="modal-header">
          <span class="modal-title">{{ editingRuleIdx >= 0 ? '编辑规则' : '添加规则' }}</span>
          <span class="modal-close" @click="showRuleModal = false">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-row">
            <div class="form-group" style="flex:1">
              <label class="form-label">规则名称</label>
              <input class="input" v-model="ruleForm.name" placeholder="如：国内直连" />
            </div>
            <div class="form-group" style="width:140px">
              <label class="form-label">动作</label>
              <select class="input" v-model="ruleForm.action">
                <option value="direct">直连</option>
                <option value="block">拒绝</option>
                <option value="outbound">指定出站</option>
              </select>
            </div>
            <div class="form-group" style="width:160px" v-if="ruleForm.action === 'outbound'">
              <label class="form-label">出站</label>
              <select class="input" v-model="ruleForm.outboundName">
                <option value="">-- 选择出站 --</option>
                <option v-for="o in outbounds" :key="o.name" :value="o.name">{{ o.name }}</option>
              </select>
            </div>
          </div>

          <!-- 入站标签 -->
          <div class="form-group">
            <label class="form-label">
              入站标签
              <span class="form-hint">匹配来自指定入站的流量，留空则匹配所有入站</span>
            </label>
            <div class="tag-input-wrap">
              <span v-for="(t, i) in ruleForm.inboundTags" :key="i" class="tag-chip">
                {{ t }} <span @click="ruleForm.inboundTags.splice(i,1)">✕</span>
              </span>
              <input class="tag-input" v-model="inboundTagInput" placeholder="输入标签后按 Enter"
                @keydown.enter.prevent="addInboundTag" />
            </div>
            <div class="quick-tags">
              <span class="quick-tag" @click="addTagIfAbsent(ruleForm.inboundTags, 'socks')">socks</span>
              <span class="quick-tag" @click="addTagIfAbsent(ruleForm.inboundTags, 'http')">http</span>
              <span v-for="ib in inbounds" :key="ib.tag" class="quick-tag"
                @click="addTagIfAbsent(ruleForm.inboundTags, ib.tag)">{{ ib.tag }}</span>
            </div>
          </div>

          <!-- 域名规则 -->
          <div class="form-group">
            <label class="form-label">
              域名规则
              <span class="form-hint">支持 geosite:cn、domain:example.com、regexp:\.cn$、keyword:google</span>
            </label>
            <textarea class="input textarea" v-model="ruleForm.domainsText"
              placeholder="每行一条，如：&#10;geosite:cn&#10;geosite:telegram&#10;domain:google.com&#10;regexp:\.cn$&#10;keyword:baidu" rows="4" />
            <div class="quick-tags" style="margin-top:4px">
              <span style="font-size:11px;color:#8c8c8c;margin-right:4px">常用：</span>
              <span v-for="t in geositePresets" :key="t" class="quick-tag geo-tag"
                @click="appendToTextarea('domainsText', 'geosite:' + t)">geosite:{{ t }}</span>
            </div>
          </div>

          <!-- IP 规则 -->
          <div class="form-group">
            <label class="form-label">
              IP 规则
              <span class="form-hint">支持 geoip:cn、CIDR（如 1.2.3.0/24）</span>
            </label>
            <textarea class="input textarea" v-model="ruleForm.ipsText"
              placeholder="每行一条，如：&#10;geoip:cn&#10;geoip:private&#10;1.2.3.0/24" rows="3" />
            <div class="quick-tags" style="margin-top:4px">
              <span style="font-size:11px;color:#8c8c8c;margin-right:4px">常用：</span>
              <span v-for="t in geoipPresets" :key="t" class="quick-tag geo-tag"
                @click="appendToTextarea('ipsText', 'geoip:' + t)">geoip:{{ t }}</span>
            </div>
          </div>

          <div class="form-row">
            <div class="form-group" style="flex:1">
              <label class="form-label">
                端口
                <span class="form-hint">如 80,443,8000-9000</span>
              </label>
              <input class="input" v-model="ruleForm.ports" placeholder="80,443" />
            </div>
            <div class="form-group" style="flex:1">
              <label class="form-label">网络协议</label>
              <select class="input" v-model="ruleForm.protocol">
                <option value="">不限</option>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
                <option value="tcp,udp">TCP+UDP</option>
              </select>
            </div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showRuleModal = false">取消</button>
          <button class="btn btn-primary" @click="saveRule">保存</button>
        </div>
      </div>
    </div>

    <!-- 添加/编辑入站弹窗 -->
    <div class="modal-overlay" v-if="showInboundModal" @click.self="showInboundModal = false">
      <div class="modal-box" style="width:480px">
        <div class="modal-header">
          <span class="modal-title">{{ editingInboundIdx >= 0 ? '编辑入站' : '添加入站' }}</span>
          <span class="modal-close" @click="showInboundModal = false">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-row">
            <div class="form-group" style="flex:1">
              <label class="form-label">名称</label>
              <input class="input" v-model="inboundForm.name" placeholder="如：透明代理" />
            </div>
            <div class="form-group" style="width:160px">
              <label class="form-label">协议</label>
              <select class="input" v-model="inboundForm.protocol">
                <option value="socks">SOCKS5</option>
                <option value="http">HTTP</option>
                <option value="dokodemo-door">透明代理</option>
              </select>
            </div>
          </div>
          <div class="form-row">
            <div class="form-group" style="flex:1">
              <label class="form-label">监听地址</label>
              <input class="input" v-model="inboundForm.listen" placeholder="127.0.0.1" />
            </div>
            <div class="form-group" style="width:120px">
              <label class="form-label">端口</label>
              <input class="input" type="number" v-model.number="inboundForm.port" placeholder="1080" />
            </div>
          </div>
          <div class="form-group">
            <label class="form-label">入站标签（用于路由规则匹配）</label>
            <input class="input" v-model="inboundForm.tag" placeholder="my-inbound" />
          </div>
          <!-- 认证字段（socks 和 http 支持） -->
          <template v-if="inboundForm.protocol === 'socks' || inboundForm.protocol === 'http'">
            <div class="form-row">
              <div class="form-group" style="flex:1">
                <label class="form-label">用户名（可选）</label>
                <input class="input" v-model="inboundForm.username" placeholder="留空表示不需要认证" />
              </div>
              <div class="form-group" style="flex:1">
                <label class="form-label">密码（可选）</label>
                <input class="input" type="password" v-model="inboundForm.password" placeholder="留空表示不需要认证" />
              </div>
            </div>
          </template>
          <template v-if="inboundForm.protocol === 'socks'">
            <div class="form-group">
              <label class="form-label" style="display:flex;align-items:center;gap:8px">
                <input type="checkbox" v-model="inboundForm.udpEnabled" />
                启用 UDP
              </label>
            </div>
          </template>
          <template v-if="inboundForm.protocol === 'dokodemo-door'">
            <div class="form-row">
              <div class="form-group" style="flex:1">
                <label class="form-label">网络</label>
                <select class="input" v-model="inboundForm.network">
                  <option value="tcp">TCP</option>
                  <option value="udp">UDP</option>
                  <option value="tcp,udp">TCP+UDP</option>
                </select>
              </div>
              <div class="form-group" style="flex:1">
                <label class="form-label" style="display:flex;align-items:center;gap:8px">
                  <input type="checkbox" v-model="inboundForm.followRedirect" />
                  跟随重定向（tproxy）
                </label>
              </div>
            </div>
          </template>
          <div class="form-group">
            <label class="form-label" style="display:flex;align-items:center;gap:8px">
              <input type="checkbox" v-model="inboundForm.sniffEnabled" />
              启用流量嗅探
            </label>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showInboundModal = false">取消</button>
          <button class="btn btn-primary" @click="saveInbound">保存</button>
        </div>
      </div>
    </div>

    <!-- 数据文件下载弹窗 -->
    <div class="modal-overlay" v-if="showDataModal" @click.self="showDataModal = false">
      <div class="modal-box" style="width:560px">
        <div class="modal-header">
          <span class="modal-title">数据文件管理</span>
          <span class="modal-close" @click="showDataModal = false">✕</span>
        </div>
        <div class="modal-body">
          <!-- geoip / geosite -->
          <div v-for="f in dataFiles.slice(0,2)" :key="f.type" class="data-file-row">
            <div class="data-file-info">
              <span class="data-file-name">{{ f.label }}</span>
              <span :class="['data-badge', f.exists ? 'data-ok' : 'data-missing']">
                {{ f.exists ? '已安装' : '未安装' }}
              </span>
              <span class="data-file-desc">{{ f.desc }}</span>
            </div>
            <div>
              <template v-if="downloading[f.type]">
                <div class="dl-progress">
                  <div class="dl-bar"><div class="dl-fill" :style="{ width: downloading[f.type].percent + '%' }"></div></div>
                  <span class="dl-msg">{{ downloading[f.type].message }}</span>
                </div>
              </template>
              <button v-else :class="['btn','btn-sm', f.exists ? 'btn-light' : 'btn-primary']"
                @click="downloadData(f.type)">
                {{ f.exists ? '更新' : '下载' }}
              </button>
            </div>
          </div>

          <!-- sing-box rule-set（展开显示每个文件） -->
          <div class="data-file-row" style="flex-direction:column;align-items:flex-start;gap:8px">
            <div style="display:flex;align-items:center;justify-content:space-between;width:100%">
              <div class="data-file-info">
                <span class="data-file-name">sing-box rule-set</span>
                <span :class="['data-badge', dataFiles[2].exists ? 'data-ok' : 'data-missing']">
                  {{ dataFiles[2].exists ? '已安装' : '未安装' }}
                </span>
                <span class="data-file-desc">本地规则集（.srs 文件，启动时无需联网）</span>
              </div>
              <div>
                <template v-if="downloading['rule-set']">
                  <div class="dl-progress">
                    <div class="dl-bar"><div class="dl-fill" :style="{ width: downloading['rule-set'].percent + '%' }"></div></div>
                    <span class="dl-msg">{{ downloading['rule-set'].message }}</span>
                  </div>
                </template>
                <button v-else :class="['btn','btn-sm', dataFiles[2].exists ? 'btn-light' : 'btn-primary']"
                  @click="downloadData('rule-set')">
                  {{ dataFiles[2].exists ? '全部更新' : '下载全部' }}
                </button>
              </div>
            </div>
            <!-- 每个 rule-set 文件的详情 -->
            <div class="ruleset-list">
              <div v-for="rs in ruleSetInfos" :key="rs.name" class="ruleset-item">
                <span class="ruleset-name">{{ rs.name }}.srs</span>
                <span v-if="rs.localDate" class="ruleset-date">本地: {{ rs.localDate }}</span>
                <span v-else class="ruleset-missing">未下载</span>
              </div>
            </div>
          </div>

          <div style="margin-top:12px;padding:10px 12px;background:#f0f7ff;border-radius:6px;font-size:12px;border:1px solid #bae0ff">
            <div style="font-weight:600;color:#1677ff;margin-bottom:6px">🚀 GitHub 加速镜像（可选）</div>
            <div style="display:flex;gap:8px;align-items:center;margin-bottom:6px">
              <input class="input" v-model="githubMirror" placeholder="如 https://ghfast.top" style="flex:1;font-size:12px" />
            </div>
            <div style="color:#8c8c8c;line-height:1.6">
              常用镜像：
              <span v-for="m in mirrorPresets" :key="m" class="mirror-preset" @click="githubMirror = m">{{ m }}</span>
              <span class="mirror-preset" style="color:#ff4d4f" @click="githubMirror = ''">清除</span>
            </div>
            <div style="color:#8c8c8c;margin-top:4px">下载 URL 将变为：<code>{{ githubMirror ? githubMirror.replace(/\/$/, '') + '/https://raw.githubusercontent.com/...' : 'https://raw.githubusercontent.com/...' }}</code></div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showDataModal = false">关闭</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, computed } from 'vue'
import { useProxyStore } from '../stores/proxy'
import { api } from '../api'

const store = useProxyStore()

const rules = ref([])
const inbounds = ref([])
const showRuleModal = ref(false)
const showInboundModal = ref(false)
const showDataModal = ref(false)
const editingRuleIdx = ref(-1)
const editingInboundIdx = ref(-1)
const inboundTagInput = ref('')
const downloading = reactive({})
const rulesFileInput = ref(null)

const socksPort = computed(() => store.setting?.socks5Port || 20260)
const httpPort = computed(() => store.setting?.httpPort || 20261)
const outbounds = computed(() => store.outbounds)

// 内置入站状态
const builtinSocks = reactive({ port: 20260, username: '', password: '' })
const builtinHttp = reactive({ port: 20261, username: '', password: '' })
const showBuiltinModal = ref(false)
const builtinEditType = ref('socks')
const builtinForm = reactive({ port: 20260, username: '', password: '' })

const dataFiles = ref([
  { type: 'geoip', label: 'geoip.dat', desc: 'IP 地理位置数据（geoip:cn 等）', exists: false },
  { type: 'geosite', label: 'geosite.dat', desc: '域名分类数据（geosite:cn 等）', exists: false },
  { type: 'rule-set', label: 'sing-box rule-set', desc: '本地规则集（geosite-cn/geoip-cn 等 .srs 文件）', exists: false },
])

const ruleSetInfos = ref([])
const githubMirror = ref('')
const mirrorPresets = [
  'https://ghfast.top',
  'https://mirror.ghproxy.com',
  'https://gh-proxy.com',
]

const defaultRuleForm = () => ({
  id: '', name: '', enabled: true, action: 'direct', outboundName: '',
  inboundTags: [], domainsText: '', ipsText: '', ports: '', protocol: '',
})

// 常用 geosite 名称（来自 MetaCubeX/meta-rules-dat）
const geositePresets = ['cn', 'gfw', 'telegram', 'google', 'youtube', 'netflix', 'openai', 'github', 'twitter', 'facebook', 'tiktok']
// 常用 geoip 名称
const geoipPresets = ['cn', 'private']

// 向 textarea 追加一行
function appendToTextarea(field, value) {
  const cur = ruleForm.value[field]
  const lines = cur ? cur.split('\n').map(s => s.trim()).filter(Boolean) : []
  if (!lines.includes(value)) {
    ruleForm.value[field] = [...lines, value].join('\n')
  }
}
const defaultInboundForm = () => ({
  id: '', name: '', tag: '', protocol: 'socks', listen: '127.0.0.1', port: 1080,
  udpEnabled: true, network: 'tcp,udp', followRedirect: false, sniffEnabled: true, sniffDest: [],
  username: '', password: '',
})

const ruleForm = ref(defaultRuleForm())
const inboundForm = ref(defaultInboundForm())

onMounted(async () => {
  await Promise.all([loadRules(), loadInbounds(), checkDataFiles()])
  store.fetchAll()
  api.getRuleSetInfos().then(r => { ruleSetInfos.value = r.data.infos || [] }).catch(() => {})
  api.getSetting().then(r => {
    const s = r.data.setting || {}
    githubMirror.value = s.githubMirror || ''
    builtinSocks.port = s.socks5Port || 20260
    builtinSocks.username = s.socks5Username || ''
    builtinSocks.password = s.socks5Password || ''
    builtinHttp.port = s.httpPort || 20261
    builtinHttp.username = s.httpUsername || ''
    builtinHttp.password = s.httpPassword || ''
  }).catch(() => {})
})

async function loadRules() {
  const { data } = await api.getRoutingRules()
  rules.value = data.rules || []
}

async function loadInbounds() {
  const { data } = await api.getCustomInbounds()
  inbounds.value = data.inbounds || []
}

async function checkDataFiles() {
  try {
    const { data } = await api.getKernelStatus()
    const kernels = data.kernels || {}
    dataFiles.value[0].exists = !!kernels['geoip']
    dataFiles.value[1].exists = !!kernels['geosite']
    const hasRuleSet = Object.keys(kernels).some(k => k.startsWith('ruleset:') && kernels[k])
    dataFiles.value[2].exists = hasRuleSet
  } catch {}
}

function actionLabel(rule) {
  if (rule.action === 'direct') return '直连'
  if (rule.action === 'block') return '拒绝'
  if (rule.action === 'outbound') return `→ ${rule.outboundName}`
  return rule.action
}

function openAddRule() {
  editingRuleIdx.value = -1
  ruleForm.value = defaultRuleForm()
  showRuleModal.value = true
}

function editRule(i) {
  editingRuleIdx.value = i
  const r = rules.value[i]
  ruleForm.value = {
    ...r,
    domainsText: (r.domains || []).join('\n'),
    ipsText: (r.ips || []).join('\n'),
  }
  showRuleModal.value = true
}

function addInboundTag() {
  const t = inboundTagInput.value.trim()
  if (t && !ruleForm.value.inboundTags.includes(t)) {
    ruleForm.value.inboundTags.push(t)
  }
  inboundTagInput.value = ''
}

function addTagIfAbsent(arr, tag) {
  if (!arr.includes(tag)) arr.push(tag)
}

async function saveRule() {
  const r = {
    ...ruleForm.value,
    domains: ruleForm.value.domainsText.split('\n').map(s => s.trim()).filter(Boolean),
    ips: ruleForm.value.ipsText.split('\n').map(s => s.trim()).filter(Boolean),
  }
  delete r.domainsText
  delete r.ipsText
  if (editingRuleIdx.value >= 0) {
    rules.value[editingRuleIdx.value] = r
  } else {
    rules.value.push(r)
  }
  await api.setRoutingRules(rules.value)
  showRuleModal.value = false
  await loadRules()
}

async function deleteRule(i) {
  rules.value.splice(i, 1)
  await api.setRoutingRules(rules.value)
}

async function toggleRule(i) {
  rules.value[i].enabled = !rules.value[i].enabled
  await api.setRoutingRules(rules.value)
}

async function moveRule(i, dir) {
  const j = i + dir
  if (j < 0 || j >= rules.value.length) return
  ;[rules.value[i], rules.value[j]] = [rules.value[j], rules.value[i]]
  await api.setRoutingRules(rules.value)
}

function triggerImportRules() {
  rulesFileInput.value?.click()
}

async function importRules(event) {
  const file = event.target.files?.[0]
  if (!file) return
  
  const formData = new FormData()
  formData.append('file', file)
  
  try {
    const response = await fetch('/api/routing-rules/import', {
      method: 'POST',
      body: formData
    })
    const data = await response.json()
    if (response.ok) {
      alert(`成功导入 ${data.count} 条规则`)
      await loadRules()
    } else {
      alert('导入失败: ' + (data.error || '未知错误'))
    }
  } catch (e) {
    alert('导入失败: ' + e.message)
  }
  
  // 重置文件输入
  event.target.value = ''
}

async function exportRules() {
  try {
    const response = await fetch('/api/routing-rules/export')
    if (!response.ok) throw new Error('导出失败')
    
    const blob = await response.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `routing-rules-${new Date().toISOString().split('T')[0]}.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  } catch (e) {
    alert('导出失败: ' + e.message)
  }
}

function editBuiltinInbound(type) {
  builtinEditType.value = type
  if (type === 'socks') {
    Object.assign(builtinForm, { port: builtinSocks.port, username: builtinSocks.username, password: builtinSocks.password })
  } else {
    Object.assign(builtinForm, { port: builtinHttp.port, username: builtinHttp.username, password: builtinHttp.password })
  }
  showBuiltinModal.value = true
}

async function saveBuiltinInbound() {
  try {
    const { data } = await api.getSetting()
    const s = { ...data.setting }
    if (builtinEditType.value === 'socks') {
      s.socks5Port = builtinForm.port
      s.socks5Username = builtinForm.username
      s.socks5Password = builtinForm.password
      Object.assign(builtinSocks, { port: builtinForm.port, username: builtinForm.username, password: builtinForm.password })
    } else {
      s.httpPort = builtinForm.port
      s.httpUsername = builtinForm.username
      s.httpPassword = builtinForm.password
      Object.assign(builtinHttp, { port: builtinForm.port, username: builtinForm.username, password: builtinForm.password })
    }
    await api.setSetting(s)
    await store.fetchAll()
    showBuiltinModal.value = false
  } catch (e) {
    alert('保存失败: ' + (e?.response?.data?.error || e.message))
  }
}

function openAddInbound() {
  editingInboundIdx.value = -1
  inboundForm.value = defaultInboundForm()
  showInboundModal.value = true
}

function editInbound(i) {
  editingInboundIdx.value = i
  inboundForm.value = { ...inbounds.value[i] }
  showInboundModal.value = true
}

async function saveInbound() {
  const ib = { ...inboundForm.value }
  if (editingInboundIdx.value >= 0) {
    inbounds.value[editingInboundIdx.value] = ib
  } else {
    inbounds.value.push(ib)
  }
  await api.setCustomInbounds(inbounds.value)
  showInboundModal.value = false
  await loadInbounds()
}

async function deleteInbound(i) {
  inbounds.value.splice(i, 1)
  await api.setCustomInbounds(inbounds.value)
}

async function downloadData(type) {
  if (type === 'rule-set') {
    downloading[type] = { percent: 0, message: '准备下载 rule-set…' }
    // 保存镜像地址到设置
    api.getSetting().then(r => {
      const s = r.data.setting || {}
      s.githubMirror = githubMirror.value
      api.setSetting(s).catch(() => {})
    }).catch(() => {})
    try {
      await api.downloadRuleSets(githubMirror.value, (p) => { downloading[type] = { percent: p.percent, message: p.message } })
      await checkDataFiles()
      api.getRuleSetInfos().then(r => { ruleSetInfos.value = r.data.infos || [] }).catch(() => {})
    } catch (e) {
      downloading[type] = { percent: 0, message: '失败: ' + e.message }
      setTimeout(() => { delete downloading[type] }, 4000)
      return
    }
    delete downloading[type]
    return
  }
  downloading[type] = { percent: 0, message: '准备中…' }
  try {
    await api.downloadData(type, (p) => { downloading[type] = { percent: p.percent, message: p.message } })
    await checkDataFiles()
  } catch (e) {
    downloading[type] = { percent: 0, message: '失败: ' + e.message }
    setTimeout(() => { delete downloading[type] }, 4000)
    return
  }
  delete downloading[type]
}
</script>

<style scoped>
.rules-page {
  display: flex;
  gap: 20px;
  padding: 20px 24px;
  height: 100%;
  overflow-y: auto;
  box-sizing: border-box;
  max-width: 1400px;
  margin: 0 auto;
  width: 100%;
}

.rules-section { flex: 1.5; min-width: 0; display: flex; flex-direction: column; gap: 12px; }
.inbounds-section { flex: 1; min-width: 280px; display: flex; flex-direction: column; gap: 12px; }

.section-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 0 0 4px;
}
.section-title { font-size: 15px; font-weight: 700; color: #262626; }
.section-actions { display: flex; align-items: center; gap: 10px; }
.hint-text { font-size: 12px; color: #8c8c8c; }

.data-files-bar {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 12px;
  background: #fafafa;
  border: 1px solid #e8e8e8;
  border-radius: 6px;
  font-size: 12px;
}
.data-label { color: #8c8c8c; }
.data-badge {
  padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 600;
}
.data-ok { background: #f6ffed; color: #52c41a; border: 1px solid #b7eb8f; }
.data-missing { background: #fff2f0; color: #ff4d4f; border: 1px solid #ffccc7; }

.rules-list, .inbounds-list {
  background: #fff;
  border: 1px solid #e8e8e8;
  border-radius: 8px;
  overflow: hidden;
}
.empty-hint {
  padding: 40px 20px; text-align: center; color: #8c8c8c; font-size: 13px;
}

.rule-row {
  display: flex; align-items: center; gap: 10px;
  padding: 10px 14px;
  border-bottom: 1px solid #f5f5f5;
  transition: background .1s;
}
.rule-row:last-child { border-bottom: none; }
.rule-row:hover { background: #fafafa; }
.rule-disabled { opacity: .5; }
.rule-drag-handle { color: #bfbfbf; cursor: grab; font-size: 16px; flex-shrink: 0; }
.rule-main { flex: 1; min-width: 0; }
.rule-top { display: flex; align-items: center; gap: 8px; margin-bottom: 4px; }
.rule-name { font-size: 13px; font-weight: 600; color: #262626; }
.rule-action-badge {
  padding: 1px 8px; border-radius: 10px; font-size: 11px; font-weight: 600;
}
.action-direct { background: #f6ffed; color: #52c41a; }
.action-block { background: #fff2f0; color: #ff4d4f; }
.action-outbound { background: #e6f4ff; color: #1677ff; }
.rule-conditions { display: flex; flex-wrap: wrap; gap: 4px; }
.cond-tag {
  padding: 1px 7px; border-radius: 3px; font-size: 11px;
  max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.cond-inbound { background: #f5f0ff; color: #7c3aed; }
.cond-domain { background: #e6f4ff; color: #1677ff; }
.cond-ip { background: #fff7e6; color: #d46b08; }
.cond-port { background: #f0fff4; color: #276749; }
.cond-proto { background: #f5f5f5; color: #595959; }

.rule-ops { display: flex; align-items: center; gap: 4px; flex-shrink: 0; }
.icon-btn {
  background: none; border: 1px solid #e8e8e8; border-radius: 4px;
  padding: 3px 7px; font-size: 12px; cursor: pointer; color: #8c8c8c;
  transition: all .1s;
}
.icon-btn:hover { background: #f5f5f5; color: #595959; }
.icon-btn:disabled { opacity: .3; cursor: not-allowed; }
.icon-btn-danger:hover { background: #fff1f0; color: #ff4d4f; border-color: #ff4d4f; }

.toggle-switch { position: relative; display: inline-block; width: 32px; height: 18px; cursor: pointer; }
.toggle-switch input { opacity: 0; width: 0; height: 0; }
.toggle-slider {
  position: absolute; inset: 0; background: #d9d9d9; border-radius: 18px; transition: .2s;
}
.toggle-slider:before {
  content: ''; position: absolute; width: 14px; height: 14px; left: 2px; bottom: 2px;
  background: #fff; border-radius: 50%; transition: .2s;
}
.toggle-switch input:checked + .toggle-slider { background: #1677ff; }
.toggle-switch input:checked + .toggle-slider:before { transform: translateX(14px); }

.inbound-row {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 14px; border-bottom: 1px solid #f5f5f5;
}
.inbound-row:last-child { border-bottom: none; }
.inbound-builtin { background: #fafafa; }
.inbound-auth-badge {
  font-size: 11px; padding: 1px 6px; border-radius: 3px;
  background: #fff7e6; color: #d46b08; border: 1px solid #ffd591;
}
.inbound-info { display: flex; align-items: center; gap: 8px; flex: 1; min-width: 0; }
.inbound-name { font-size: 13px; font-weight: 600; color: #262626; }
.inbound-proto-badge {
  padding: 1px 7px; border-radius: 3px; font-size: 11px; font-weight: 600;
  background: #e6f4ff; color: #1677ff;
}
.inbound-addr { font-size: 12px; color: #8c8c8c; font-family: monospace; }
.inbound-tag-badge {
  font-size: 11px; color: #7c3aed; background: #f5f0ff; padding: 1px 6px; border-radius: 3px;
}

.form-row { display: flex; gap: 12px; }
.form-row .form-group { margin-bottom: 14px; }
.form-hint { font-size: 11px; color: #8c8c8c; font-weight: 400; margin-left: 6px; }
.textarea { resize: vertical; min-height: 80px; font-family: monospace; font-size: 12px; }

.tag-input-wrap {
  display: flex; flex-wrap: wrap; gap: 4px; align-items: center;
  border: 1px solid #dbdbdb; border-radius: 4px; padding: 5px 8px; min-height: 36px;
  background: #fff; cursor: text;
}
.tag-chip {
  display: inline-flex; align-items: center; gap: 4px;
  background: #e6f4ff; color: #1677ff; padding: 1px 8px; border-radius: 10px; font-size: 12px;
}
.tag-chip span { cursor: pointer; font-size: 10px; }
.tag-input { border: none; outline: none; font-size: 13px; flex: 1; min-width: 80px; }
.quick-tags { display: flex; flex-wrap: wrap; gap: 4px; margin-top: 4px; }
.quick-tag {
  padding: 1px 8px; border-radius: 10px; font-size: 11px; cursor: pointer;
  background: #f5f5f5; color: #595959; border: 1px solid #e8e8e8;
}
.quick-tag:hover { background: #e6f4ff; color: #1677ff; border-color: #91caff; }

.data-file-row {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 0; border-bottom: 1px solid #f0f0f0;
}
.data-file-row:last-child { border-bottom: none; }
.data-file-info { display: flex; align-items: center; gap: 8px; flex: 1; }
.data-file-name { font-size: 13px; font-weight: 600; font-family: monospace; }
.data-file-desc { font-size: 12px; color: #8c8c8c; }
.ruleset-list {
  display: flex; flex-wrap: wrap; gap: 6px; width: 100%;
}
.ruleset-item {
  display: flex; align-items: center; gap: 6px;
  padding: 3px 10px; background: #f5f5f5; border-radius: 4px; font-size: 11px;
}
.ruleset-name { font-family: monospace; color: #262626; }
.ruleset-date { color: #52c41a; }
.ruleset-missing { color: #ff4d4f; }
.mirror-preset {
  display: inline-block; margin: 0 4px 2px 0;
  padding: 1px 8px; border-radius: 10px; cursor: pointer;
  background: #e6f4ff; color: #1677ff; font-size: 11px; font-family: monospace;
}
.mirror-preset:hover { background: #bae0ff; }
.geo-tag {
  background: #f0f7ff; color: #1677ff; border-color: #91caff;
  font-family: monospace;
}
.geo-tag:hover { background: #bae0ff; }
.dl-progress { display: flex; flex-direction: column; gap: 4px; min-width: 120px; }
.dl-bar { height: 4px; background: #f0f0f0; border-radius: 2px; overflow: hidden; }
.dl-fill { height: 100%; background: #1677ff; border-radius: 2px; transition: width .3s; }
.dl-msg { font-size: 11px; color: #8c8c8c; }

.action-btn {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 5px 12px; border: 1px solid #d9d9d9; border-radius: 6px;
  background: #fff; color: #595959; font-size: 12px; cursor: pointer; transition: all .15s;
}
.action-btn:hover { border-color: #1677ff; color: #1677ff; background: #f5f8ff; }
.action-btn-primary { background: #1677ff; color: #fff; border-color: #1677ff; }
.action-btn-primary:hover { background: #0958d9; border-color: #0958d9; color: #fff; }
</style>
