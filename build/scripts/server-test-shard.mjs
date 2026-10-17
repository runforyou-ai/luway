import { spawnSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

// 慢用例的基线秒数用于分配两组负载，其余及新增用例权重为 1。
const weights = {
  "TestGatewayExecutorLifecycle": 62,
  "TestTaskRuntime": 31,
  "TestRealtimePeriodicCheckpoint": 10,
  "TestRealtimeGroupMembershipNotifications": 10,
  "TestRealtimeConversationStateNotifications": 10,
  "TestRealtimeGatewayDelivery": 11,
  "TestGroupAgentMentionReplies": 10,
  "TestGatewayVisitorExpiryRefresh": 20,
  "TestGatewayVisitorIdentityRevocation": 21,
  "TestGatewayRunAdmissionOrder": 21,
  "TestGatewayRunBusinessGapAndSameNodeTakeover": 21,
  "TestGatewayRunSnapshotIntegrityAndDeadline": 17,
  "TestGatewayRunActualModelAndCancellation": 16,
  "TestGatewayRunReconnectRecovery": 15,
  "TestGatewayRunPublicationFailureAndCrash": 15,
  "TestGatewayMembershipChanges": 10,
  "TestAdmitInstance": 18,
  "TestAgentCustomerReplies": 15,
  "TestAgentConversations": 120
};

// partition 将当前发现的用例完整、互斥地分配到 A、B 两组。
export function partition(names) {
  const groups = [[], []];
  const loads = [0, 0];
  const unique = [...new Set(names)].sort((a, b) =>
    (weights[b] ?? 1) - (weights[a] ?? 1) || (a < b ? -1 : a > b ? 1 : 0));
  for (const name of unique) {
    const index = loads[0] <= loads[1] ? 0 : 1;
    groups[index].push(name);
    loads[index] += weights[name] ?? 1;
  }
  if (groups.some(group => group.length === 0)) {
    throw new Error('测试分组为空');
  }
  return groups;
}

// main 按当前构建标签发现测试并输出精确匹配所选分组的表达式。
function main() {
  const shard = process.argv[2];
  if (!['A', 'B'].includes(shard)) throw new Error('测试分组必须为 A 或 B');
  const packages = process.argv.slice(3);
  if (packages.length === 0) throw new Error('必须指定测试包范围');
  const listing = spawnSync('go', ['test', '-list', '.', '-tags=server,testhash', ...packages], {
    encoding: 'utf8', maxBuffer: 16 * 1024 * 1024,
  });
  if (listing.error) throw listing.error;
  if (listing.status !== 0) {
    process.stderr.write(listing.stdout + listing.stderr);
    process.exit(listing.status ?? 1);
  }
  const names = listing.stdout.split(/\r?\n/).filter(name => /^(Test|Example|Fuzz)\S*$/.test(name));
  const groups = partition(names);
  const selected = groups[shard === 'A' ? 0 : 1];
  console.error(`测试分组 ${shard}：${selected.length}/${new Set(names).size} 个顶层用例，并发 12`);
  console.error(selected.join('\n'));
  // Go 测试名称是标识符，表达式仅匹配顶层名称，包含其全部子测试。
  console.log(`^(${selected.join('|')})$`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
