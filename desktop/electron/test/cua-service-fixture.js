"use strict";
const net = require("node:net");
const fs = require("node:fs");
const args = process.argv.slice(2);
const socket = args[args.indexOf("--socket") + 1];
const deferred = args.includes("--defer-permissions");
process.umask(deferred ? 0o022 : 0o177);
const server = net.createServer(peer => {
  if ((fs.statSync(socket).mode & 0o777) !== 0o600) process.exit(2);
  peer.on("data", () => peer.end(JSON.stringify({ ok: true, result: { pid: process.pid, embedded: true, host_bundle_id: "io.reasonix.studio", driver_version: "0.34.1", contract_version: "0.8.0", tools_list_schema_version: "1", capability_version: "1", mcp_protocol_version: "2025-06-18" } }) + "\n"));
});
server.listen(socket);
process.on("message", message => {
  if (message === "secure") fs.chmodSync(socket, 0o600);
});
process.stdin.resume();
process.stdin.on("end", () => server.close(() => process.exit(0)));
