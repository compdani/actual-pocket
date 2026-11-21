// Fake bank providers for the browser acceptance check. Run with:
//   node e2e/fake-providers.mjs
import http from 'node:http';

const basePort = Number(process.env.FAKE_PORT || 6100);
const yesterday = new Date(Date.now() - 24 * 60 * 60 * 1000);
const fixtureTime = yesterday.toISOString();
const fixtureDate = fixtureTime.slice(0, 10);

function json(res, status, body) {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(body));
}

function text(res, status, body) {
  res.writeHead(status, { 'Content-Type': 'text/plain' });
  res.end(body);
}

function start(name, port, handler) {
  const server = http.createServer(handler);
  server.listen(port, '127.0.0.1', () => {
    console.log(`${name} fake on http://127.0.0.1:${port}`);
  });
  return server;
}

// SimpleFIN
start('simplefin', basePort + 1, (req, res) => {
  if (req.url === '/claim' && req.method === 'POST') {
    text(res, 200, 'https://user:password@bridge.example');
    return;
  }
  if (req.url.startsWith('/accounts')) {
    json(res, 200, {
      accounts: [
        {
          id: 'simplefin-account-1',
          name: 'SimpleFIN Checking',
          org: { name: 'SimpleFIN Bank', domain: 'simplefin.example', id: 'org-1' },
          balance: '100.00',
          'balance-date': Math.floor(yesterday.getTime() / 1000),
          currency: 'USD',
          transactions: [
            {
              id: 'simplefin-tx-1',
              payee: 'Coffee Shop',
              description: 'Coffee',
              amount: '-4.50',
              posted: Math.floor(yesterday.getTime() / 1000),
            },
          ],
        },
      ],
      errors: [],
    });
    return;
  }
  text(res, 404, 'not found');
});

// Enable Banking
start('enablebanking', basePort + 2, (req, res) => {
  if (req.url === '/application') {
    json(res, 200, { id: 'app' });
    return;
  }
  if (req.url.startsWith('/aspsps')) {
    json(res, 200, {
      aspsps: [{ name: 'Nordea', country: 'FI', psu_types: ['personal'] }],
    });
    return;
  }
  if (req.url === '/auth' && req.method === 'POST') {
    let body = '';
    req.on('data', chunk => (body += chunk));
    req.on('end', () => {
      const parsed = JSON.parse(body);
      json(res, 200, {
        url: `${process.env.PUBLIC_URL}/enablebanking/auth_callback?code=eb-code&state=${parsed.state}`,
        authorization_id: 'auth-1',
      });
    });
    return;
  }
  if (req.url === '/sessions' && req.method === 'POST') {
    json(res, 200, {
      session_id: 'eb-session-1',
      accounts: [
        {
          account_id: { iban: 'FI0455231152453547' },
          account_servicer: { bic_fi: 'NDEAFIHH', name: 'Nordea' },
          name: 'Enable Banking Checking',
          currency: 'EUR',
          uid: 'enablebanking-account-1',
        },
      ],
      aspsp: { name: 'Nordea', country: 'FI' },
    });
    return;
  }
  if (req.url.endsWith('/balances')) {
    json(res, 200, {
      balances: [
        {
          balance_amount: { currency: 'EUR', amount: '250.00' },
          balance_type: 'CLAV',
          reference_date: fixtureDate,
        },
      ],
    });
    return;
  }
  if (req.url.includes('/transactions')) {
    json(res, 200, {
      transactions: [
        {
          entry_reference: 'eb-tx-1',
          transaction_amount: { currency: 'EUR', amount: '-12.00' },
          creditor: { name: 'Enable Banking Merchant' },
          credit_debit_indicator: 'DBIT',
          status: 'BOOK',
          booking_date: fixtureDate,
          remittance_information: ['Card purchase'],
        },
      ],
    });
    return;
  }
  text(res, 404, 'not found');
});

// Pluggy.ai
start('pluggyai', basePort + 3, (req, res) => {
  if (req.url === '/auth' && req.method === 'POST') {
    json(res, 200, { apiKey: 'pluggy-key' });
    return;
  }
  if (req.url.startsWith('/accounts?itemId=')) {
    json(res, 200, {
      results: [
        {
          id: 'pluggy-account-1',
          itemId: 'item-1',
          name: 'Pluggy Checking',
          type: 'BANK',
          taxNumber: '123',
          owner: 'Pluggy Owner',
          balance: 300,
          currencyCode: 'BRL',
          bankData: { automaticallyInvestedBalance: 0, closingBalance: 300 },
        },
      ],
      total: 1,
    });
    return;
  }
  const pluggyURL = new URL(req.url, 'http://localhost');
  if (
    pluggyURL.pathname === '/v2/transactions' &&
    pluggyURL.searchParams.get('accountId') === 'pluggy-account-1'
  ) {
    json(res, 200, {
      results: [
        {
          id: 'pluggy-tx-1',
          date: fixtureTime,
          description: 'Pluggy purchase',
          descriptionRaw: 'Pluggy Merchant',
          amount: -15,
          currencyCode: 'BRL',
          status: 'POSTED',
          type: 'DEBIT',
          merchant: { name: 'Pluggy Merchant' },
        },
      ],
      next: null,
    });
    return;
  }
  if (req.url === '/accounts/pluggy-account-1') {
    json(res, 200, {
      id: 'pluggy-account-1',
      itemId: 'item-1',
      name: 'Pluggy Checking',
      type: 'BANK',
      balance: 300,
      currencyCode: 'BRL',
      updatedAt: fixtureTime,
    });
    return;
  }
  text(res, 404, 'not found');
});

// Akahu
start('akahu', basePort + 4, (req, res) => {
  if (req.url.startsWith('/accounts/akahu-account-1/transactions/pending')) {
    json(res, 200, { success: true, items: [] });
    return;
  }
  if (req.url.startsWith('/accounts/akahu-account-1/transactions')) {
    json(res, 200, {
      success: true,
      cursor: {},
      items: [
        {
          _id: 'akahu-tx-1',
          date: fixtureTime,
          amount: -9,
          description: 'Akahu purchase',
          merchant: { name: 'Akahu Merchant' },
        },
      ],
    });
    return;
  }
  if (req.url === '/accounts/akahu-account-1') {
    json(res, 200, {
      success: true,
      item: {
        _id: 'akahu-account-1',
        name: 'Akahu Checking',
        connection: { _id: 'akahu-connection-1', name: 'Akahu Bank' },
        balance: { current: 400, available: 400, currency: 'NZD' },
        refreshed: {
          balance: new Date().toISOString(),
          transactions: new Date().toISOString(),
        },
      },
    });
    return;
  }
  if (req.url === '/accounts') {
    json(res, 200, {
      success: true,
      items: [
        {
          _id: 'akahu-account-1',
          name: 'Akahu Checking',
          connection: { _id: 'akahu-connection-1', name: 'Akahu Bank' },
          balance: { current: 400, available: 400, currency: 'NZD' },
          refreshed: {
            balance: new Date().toISOString(),
            transactions: new Date().toISOString(),
          },
        },
      ],
    });
    return;
  }
  text(res, 404, 'not found');
});
