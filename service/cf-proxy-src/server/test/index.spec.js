import {
	env,
	createExecutionContext,
	waitOnExecutionContext,
	SELF,
} from "cloudflare:test";
import { describe, it, expect } from "vitest";
import worker from "../src";

describe("TCP proxy worker", () => {
	it("returns version metadata (unit style)", async () => {
		const request = new Request("http://example.com/api/version");
		const ctx = createExecutionContext();
		const response = await worker.fetch(request, env, ctx);
		await waitOnExecutionContext(ctx);
		expect(response.status).toBe(200);
		await expect(response.json()).resolves.toMatchObject({
			worker: "tcp",
			strategy: "prefer_ipv4",
			compat: "nodejs_compat",
		});
	});

	it("rejects unknown routes (unit style)", async () => {
		const request = new Request("http://example.com");
		const ctx = createExecutionContext();
		const response = await worker.fetch(request, env, ctx);
		await waitOnExecutionContext(ctx);
		expect(response.status).toBe(404);
		expect(await response.text()).toBe("Not Found");
	});

	it("rejects non-WebSocket TCP requests (integration style)", async () => {
		const response = await SELF.fetch("http://example.com/api/tcp?k=x&h=example.com&p=80");
		expect(response.status).toBe(426);
		expect(await response.text()).toBe("Expected Upgrade: websocket");
	});
});
