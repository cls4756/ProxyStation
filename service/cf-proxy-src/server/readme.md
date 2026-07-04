1.npm install
2.npx wrangler login
3. npx wrangler d1 create tcp_proxy_db
4.  npx wrangler d1 execute tcp_proxy_db --remote --file=schema.sql
npm run deploy

查看cf错误日志：
npx wrangler tail

修改后重新部署
npm run deploy