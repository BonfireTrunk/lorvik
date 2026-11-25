// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";

export function sessionLabel(id: string): string {
  return `kilo-${createHash("sha256").update(id).digest("hex").slice(0,24)}`;
}
