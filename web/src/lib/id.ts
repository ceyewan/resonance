export type IdValue = bigint | number | string;

export function toBigIntId(value: IdValue): bigint {
  if (typeof value === "bigint") {
    return value;
  }
  if (typeof value === "number") {
    if (!Number.isInteger(value)) {
      throw new Error("ID number must be an integer");
    }
    return BigInt(value);
  }

  const normalized = value.trim();
  if (!/^-?\d+$/.test(normalized)) {
    throw new Error(`Invalid ID string: ${value}`);
  }
  return BigInt(normalized);
}

export function toIdString(value: IdValue): string {
  return toBigIntId(value).toString();
}
