import { v7 as uuidv7 } from "uuid";

export function createID(): string {
  return uuidv7();
}
