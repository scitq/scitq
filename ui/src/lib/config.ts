type Cfg = { apiHttp: string; apiGrpc: string; apiWs: string };

// 1) Same-origin defaults (idea #1)
const loc = typeof location !== 'undefined' ? location : ({ protocol: 'https:', host: '' } as Location);
export const CONFIG: Cfg = {
  apiHttp: `${loc.protocol}//${loc.host}`,
  apiGrpc: `${loc.protocol}//${loc.host}`,
  apiWs:   `${loc.protocol === 'https:' ? 'wss:' : 'ws:'}//${loc.host}`,
};
