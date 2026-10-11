// partitionNumber turns a partition's kernel name into the number that
// DELETE /disks/:device/partitions/:number takes. The kernel puts a "p"
// between the disk and the number when the disk's name ends in a digit, so
// partition 3 is sda3 on sda but nvme0n1p3 on nvme0n1. Null when the name is
// not a partition of that disk.
//
// The page used to send the name itself, which the server refuses as an
// invalid number, so deleting a partition had never worked from here.
export function partitionNumber(disk: string, partition: string): string | null {
  const prefix = /\d$/.test(disk) ? `${disk}p` : disk
  if (!partition.startsWith(prefix)) return null
  const rest = partition.slice(prefix.length)
  return /^[1-9]\d*$/.test(rest) ? rest : null
}
