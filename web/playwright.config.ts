// Copyright (c) 2026 Vassel Goleyu. MIT License.
import { defineConfig } from '@playwright/test';

const mock = 'http://127.0.0.1:4010';

export default defineConfig({
	testMatch: '**/*.e2e.{ts,js}',
	timeout: 60_000,
	use: { baseURL: 'http://localhost:4173' },
	webServer: [
		{
			command: 'node e2e/mock-server.mjs',
			url: `${mock}/__calls`,
			reuseExistingServer: !process.env.CI
		},
		{
			command: 'npm run build && npm run preview',
			url: 'http://localhost:4173',
			timeout: 180_000,
			reuseExistingServer: !process.env.CI,
			env: {
				API_URL: mock,
				PUBLIC_API_URL: mock,
				OIDC_ISSUER: `${mock}/oidc`,
				TILE_URL: `${mock}/tiles/{z}/{x}/{y}.png`
			}
		}
	]
});
