import fs from 'node:fs';

import * as api from '@actual-app/api';
fs.mkdirSync('/tmp/e2e-api/client', { recursive: true });
const internal = await api.init({
  dataDir: '/tmp/e2e-api/client',
  serverURL: 'http://127.0.0.1:5091',
  email: 'e2e@example.com',
  password: 'e2e-password',
});
await internal.send('create-budget', { budgetName: 'Smoke Budget' });
await internal.send('preferences/save', {
  id: 'flags.enableBanking',
  value: 'true',
});
await internal.send('api/sync');
const budgets = await internal.send('api/get-budgets');
console.log(JSON.stringify(budgets));
await api.shutdown();
