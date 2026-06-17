import DockerStacks from '@/pages/docker/DockerStacks'

// Cluster › Docker is the cluster-wide master-detail. It reuses the Docker
// Stacks page in clusterMode: the left list aggregates every node's stacks
// grouped by node, and selecting one shows the same full detail panel
// (services / editor / logs / actions) scoped to that stack's node.
export default function ClusterStacks() {
  return <DockerStacks clusterMode />
}
