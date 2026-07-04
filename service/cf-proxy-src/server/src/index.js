import { DurableObject } from 'cloudflare:workers';
import { connect } from 'cloudflare:sockets';
import dns from 'node:dns/promises';

// 默认私钥仅用于本地调试，生产环境请用 wrangler secret: SECRET_KEY
const DEFAULT_SECRET_KEY = 'addfkjwl-32asd';
const TIME_WINDOW_MS = 30000;
const RESOLUTION_STRATEGY = 'prefer_ipv4'; // ipv4_only | prefer_ipv4 | prefer_ipv6 | auto
const CONNECT_TIMEOUT_MS = 8000;
const BUILD_MARKER = 'dns-fallback-v2';

// ============================================================
//  TcpProxy Durable Object — 承载所有 WebSocket ↔ TCP 代理逻辑
// ============================================================
export class TcpProxy extends DurableObject {
	constructor(ctx, env) {
		super(ctx, env);
		this.env = env;
	}

	/**
	 * 处理 WebSocket 升级请求，桥接 WebSocket ↔ TCP
	 */
	async fetch(request) {
		return handleTcpProxyRequest(request, this.env, 'do');
	}

	/**
	 * RPC: 清理此 DO 实例的所有存储（供 Worker 调用）
	 */
	async cleanup() {
		await this.ctx.storage.deleteAll();
		return { ok: true };
	}
}

// ============================================================
//  Worker 入口空壳 — 仅做路由分发
// ============================================================
export default {
	async fetch(request, env) {
		const url = new URL(request.url);
		const path = url.pathname;

		if (path === '/api/version') {
			return Response.json({
				worker: 'tcp',
				version: BUILD_MARKER,
				strategy: RESOLUTION_STRATEGY,
				compat: 'nodejs_compat',
				now: new Date().toISOString(),
			});
		}

		// ---- WebSocket TCP 代理 ----
		if (path === '/api/tcp') {
			const h = url.searchParams.get('h');
			const p = url.searchParams.get('p');
			const mode = (url.searchParams.get('m') || url.searchParams.get('mode') || 'direct').toLowerCase();
			const doName = (url.searchParams.get('doid') || '').trim();

			if (mode !== 'do') {
				return handleTcpProxyRequest(request, env, 'worker');
			}

			// DO 模式：优先按 doid 复用分片；未提供 doid 时回退为唯一实例
			const id = doName ? env.TCP_PROXY.idFromName(doName) : env.TCP_PROXY.newUniqueId();
			const stub = env.TCP_PROXY.get(id);
			url.searchParams.set('doid', id.toString());
			const doRequest = new Request(url.toString(), request);

			// 将实例 ID 记录到 D1
			const target = h && p ? `${h}:${p}` : 'unknown';
			try {
				await env.DB.prepare(
					'INSERT OR REPLACE INTO do_instances (id, created_at, target) VALUES (?, ?, ?)'
				).bind(id.toString(), new Date().toISOString(), target).run();
			} catch (e) {
				console.error('[D1] Failed to record DO instance:', e.message || e);
				// 不阻塞主流程，继续转发
			}

			// 转发请求到 DO
			return stub.fetch(doRequest);
		}

		// ---- 清理 API: 列出所有已记录的 DO 实例 ----
		if (path === '/api/cleanup/list' && request.method === 'GET') {
			try {
				const result = await env.DB.prepare(
					'SELECT id, created_at, target FROM do_instances ORDER BY created_at DESC'
				).all();
				return Response.json({
					count: result.results.length,
					instances: result.results
				});
			} catch (e) {
				return Response.json({ error: e.message }, { status: 500 });
			}
		}

		// ---- 清理 API: 批量清理所有 DO 实例 ----
		if (path === '/api/cleanup' && request.method === 'POST') {
			try {
				const result = await env.DB.prepare(
					'SELECT id FROM do_instances'
				).all();

				const ids = result.results.map(r => r.id);
				let cleaned = 0;
				let failed = 0;
				const errors = [];

				for (const hexId of ids) {
					try {
						const doId = env.TCP_PROXY.idFromString(hexId);
						const stub = env.TCP_PROXY.get(doId);
						await stub.cleanup();

						// 清理成功，从 D1 删除记录
						await env.DB.prepare(
							'DELETE FROM do_instances WHERE id = ?'
						).bind(hexId).run();
						cleaned++;
					} catch (e) {
						failed++;
						errors.push({ id: hexId, error: e.message });
					}
				}

				return Response.json({ cleaned, failed, errors });
			} catch (e) {
				return Response.json({ error: e.message }, { status: 500 });
			}
		}

		return new Response('Not Found', { status: 404 });
	}
};

// ============================================================
//  工具函数
// ============================================================

async function handleTcpProxyRequest(request, env, runtime) {
	try {
		const url = new URL(request.url);
		const k = url.searchParams.get('k');
		const h = url.searchParams.get('h');
		const p = url.searchParams.get('p');
		const doID = runtime === 'do' ? url.searchParams.get('doid') : '';

		if (!k || !h || !p) {
			return new Response('Missing required parameters: k, h, p', { status: 400 });
		}

		const upgradeHeader = request.headers.get('Upgrade');
		if (!upgradeHeader || upgradeHeader.toLowerCase() !== 'websocket') {
			return new Response('Expected Upgrade: websocket', { status: 426 });
		}

		const secret = env?.SECRET_KEY || DEFAULT_SECRET_KEY;
		const isAuthorized = await verifyTimeBasedKey(k, secret);
		if (!isAuthorized) {
			return new Response('Forbidden: Invalid or Expired Key', { status: 403 });
		}

		const portMsg = parseInt(p, 10);
		if (isNaN(portMsg) || portMsg <= 0 || portMsg > 65535) {
			return new Response('Invalid port', { status: 400 });
		}

		let hostname = h;
		if (hostname.startsWith('[') && hostname.endsWith(']')) {
			hostname = hostname.slice(1, -1);
		}
		if (hostname.includes(':')) {
			hostname = `${hostname.replace(/:/g, '-')}.sslip.io`;
		}

		const { socket: tcpSocket, connectedTarget } = await connectWithFallback(hostname, portMsg, RESOLUTION_STRATEGY);
		console.log('[TCP] connected', { runtime, hostname, port: portMsg, connectedTarget, strategy: RESOLUTION_STRATEGY });

		tcpSocket.closed.catch((err) => {
			if (err?.message?.includes('currently being piped to')) {
				return;
			}
			console.error('[TCP] socket closed with error:', err.message || err);
		});

		const webSocketPair = new WebSocketPair();
		const [client, server] = Object.values(webSocketPair);

		server.accept();

		const webSocketToTcpStream = new ReadableStream({
			start(controller) {
				server.addEventListener('message', (event) => {
					let data = event.data;
					if (typeof data === 'string') {
						data = new TextEncoder().encode(data);
					}
					controller.enqueue(data);
				});
				server.addEventListener('close', () => controller.close());
				server.addEventListener('error', (event) => {
					controller.error(new Error(event.message || 'WebSocket error'));
				});
			},
			cancel() {
				try { server.close(); } catch (e) { }
			}
		});

		const tcpToWebSocketStream = new WritableStream({
			write(chunk) {
				server.send(chunk);
			},
			close() {
				try { server.close(); } catch (e) { }
			},
			abort(err) {
				try { server.close(); } catch (e) { }
			}
		});

		const pipeSettled = makeOnce(async () => {
			if (runtime === 'do' && doID) {
				await deleteDOInstanceRecord(env, doID);
			}
		});

		webSocketToTcpStream
			.pipeTo(tcpSocket.writable)
			.catch((err) => {
				console.error('[WS->TCP] pipe failed:', err?.stack || err?.message || err);
			})
			.finally(pipeSettled);

		tcpSocket.readable
			.pipeTo(tcpToWebSocketStream)
			.catch((err) => {
				console.error('[TCP->WS] pipe failed:', err?.stack || err?.message || err);
			})
			.finally(pipeSettled);

		tcpSocket.closed.finally(pipeSettled);

		return new Response(null, {
			status: 101,
			webSocket: client,
		});
	} catch (error) {
		const detail = formatError(error);
		console.error('[TCP] unhandled proxy error:', detail);
		return new Response(`TCP Proxy Error: ${detail}`, { status: 500 });
	}
}

function makeOnce(fn) {
	let called = false;
	return async () => {
		if (called) return;
		called = true;
		try {
			await fn();
		} catch (error) {
			console.error('[DO] cleanup hook failed:', error?.message || error);
		}
	};
}

async function deleteDOInstanceRecord(env, id) {
	if (!env?.DB || !id) return;
	try {
		await env.DB.prepare('DELETE FROM do_instances WHERE id = ?').bind(id).run();
		console.log('[D1] deleted DO instance record', { id });
	} catch (error) {
		console.error('[D1] Failed to delete DO instance record:', error?.message || error);
	}
}

/**
 * 验证基于时间生成的 Key
 * 格式: "时间戳-HMAC签名"
 */
async function verifyTimeBasedKey(k, secret) {
	try {
		const parts = k.split('-');
		if (parts.length !== 2) return false;

		const timestampStr = parts[0];
		const providedSignature = parts[1];
		const timestamp = parseInt(timestampStr, 10);

		if (isNaN(timestamp)) return false;

		// 检查时间是否在允许的窗口内
		const now = Date.now();
		if (Math.abs(now - timestamp) > TIME_WINDOW_MS) {
			return false;
		}

		// 重新计算 HMAC 进行比对
		const encoder = new TextEncoder();
		const keyMaterial = await crypto.subtle.importKey(
			"raw",
			encoder.encode(secret),
			{ name: "HMAC", hash: "SHA-256" },
			false,
			["sign"]
		);

		const signatureBuffer = await crypto.subtle.sign(
			"HMAC",
			keyMaterial,
			encoder.encode(timestampStr)
		);

		const hashArray = Array.from(new Uint8Array(signatureBuffer));
		const expectedSignature = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');

		return providedSignature === expectedSignature;
	} catch (e) {
		return false;
	}
}

async function resolveTargetCandidates(hostname, strategy) {
	if (isIPv4(hostname)) {
		return [{ address: hostname, family: 4 }];
	}
	if (isIPv6(hostname)) {
		return [{ address: hostname, family: 6 }];
	}

	const [ipv4List, ipv6List] = await Promise.all([
		resolveDNS(hostname, 'A'),
		strategy === 'ipv4_only' ? Promise.resolve([]) : resolveDNS(hostname, 'AAAA'),
	]);

	const ordered = [];
	switch (strategy) {
		case 'ipv4_only':
			pushCandidates(ordered, ipv4List, 4);
			break;
		case 'prefer_ipv6':
			pushCandidates(ordered, ipv6List, 6);
			pushCandidates(ordered, ipv4List, 4);
			break;
		case 'auto':
			interleaveCandidates(ordered, ipv4List, ipv6List);
			break;
		case 'prefer_ipv4':
		default:
			pushCandidates(ordered, ipv4List, 4);
			pushCandidates(ordered, ipv6List, 6);
			break;
	}

	if (ordered.length === 0) {
		throw new Error(`DNS resolve returned no usable addresses for ${hostname}`);
	}
	return ordered;
}

async function resolveDNS(hostname, type) {
	try {
		if (type === 'A') {
			return await dns.resolve4(hostname);
		}
		if (type === 'AAAA') {
			return await dns.resolve6(hostname);
		}
		return [];
	} catch (error) {
		console.warn('[DNS] resolve failed', { hostname, type, error: formatError(error) });
		return [];
	}
}

function pushCandidates(target, addresses, family) {
	for (const address of addresses || []) {
		target.push({ address, family });
	}
}

function interleaveCandidates(target, ipv4List, ipv6List) {
	const max = Math.max(ipv4List.length, ipv6List.length);
	for (let i = 0; i < max; i++) {
		if (i < ipv4List.length) {
			target.push({ address: ipv4List[i], family: 4 });
		}
		if (i < ipv6List.length) {
			target.push({ address: ipv6List[i], family: 6 });
		}
	}
}

async function connectWithFallback(hostname, port, strategy) {
	const failures = [];

	try {
		console.log('[TCP] trying hostname', { hostname, port });
		const socket = connect({ hostname, port });
		await socket.opened;
		return { socket, connectedTarget: `${hostname}:${port}` };
	} catch (error) {
		failures.push({
			address: hostname,
			family: 'hostname',
			port,
			error: formatError(error),
		});
	}

	const candidates = await resolveTargetCandidates(hostname, strategy);
	for (const candidate of candidates) {
		try {
			console.log('[TCP] trying candidate', { address: candidate.address, family: candidate.family, port });
			const socket = connect({ hostname: candidate.address, port });
			await socket.opened;
			return { socket, connectedTarget: `${candidate.address}:${port}` };
		} catch (error) {
			failures.push({
				address: candidate.address,
				family: candidate.family,
				port,
				error: formatError(error),
			});
		}
	}
	throw new Error(`All connect attempts failed: ${failures.map(f => `${f.address}:${f.port} family=${f.family} err=${f.error}`).join(' || ')}`);
}

function isIPv4(value) {
	return /^(?:\d{1,3}\.){3}\d{1,3}$/.test(value);
}

function isIPv6(value) {
	return value.includes(':');
}

function formatError(error) {
	if (!error) {
		return 'Unknown';
	}
	const lines = [];
	const mainName = error?.name || 'Error';
	const mainMsg = error?.message || String(error);
	lines.push(`${mainName}: ${mainMsg}`);
	if (Array.isArray(error?.errors) && error.errors.length > 0) {
		for (const sub of error.errors) {
			const subName = sub?.name || 'Error';
			const subMsg = sub?.message || String(sub);
			const addr = sub?.address ? ` address=${sub.address}` : '';
			const port = sub?.port ? ` port=${sub.port}` : '';
			const code = sub?.code ? ` code=${sub.code}` : '';
			lines.push(`caused by ${subName}: ${subMsg}${code}${addr}${port}`);
		}
	}
	if (error?.stack) {
		const stackLine = String(error.stack).split('\n').slice(0, 4).join(' | ');
		lines.push(`stack=${stackLine}`);
	}
	return lines.join(' ; ');
}
