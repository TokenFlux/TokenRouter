/**
 * rechargeCredit.ts calculates recharge previews from decimal principal and rate
 * values before validating their wallet storage range. The backend converts each
 * floating input to its shortest decimal representation and rounds the product
 * to two places. Multiplying binary numbers first can instead move a half-cent
 * result below the rounding boundary and understate the expected credit.
 *
 * Decimal coefficients and exponents preserve the input values through integer
 * multiplication. Positive half-cent ties round upward, including scientific
 * notation inputs. The resulting number remains a preview: checkout rereads the
 * account terms, and existing orders retain their saved entitlement. Range
 * validation follows the redemption and wallet numeric columns rather than the
 * wider payment-order amount column.
 */

// Wallet and redemption values use numeric(20,8), so their twelve integer digits
// impose this exclusive ceiling on individual rounded recharge credits.
const BALANCE_CREDIT_CEILING = 1e12

/**
 * calculateRechargeCredit multiplies decimal input values and rounds to two places.
 * Nonfinite or negative principal and nonpositive rates return NaN; zero principal
 * returns zero for an empty checkout preview.
 *
 * Integer cent scaling precedes rounding so input multiplication cannot introduce
 * binary half-cent errors. The operands use their shortest decimal representations,
 * matching backend conversion. Very large results can return Infinity, which
 * isValidRechargeCredit rejects before the page enables payment submission.
 */
export function calculateRechargeCredit(principal: number, multiplier: number): number {
  if (
    !Number.isFinite(principal) ||
    principal < 0 ||
    !Number.isFinite(multiplier) ||
    multiplier <= 0
  ) {
    return Number.NaN
  }
  const [principalCoefficient, principalExponent] = decimalParts(principal)
  const [rateCoefficient, rateExponent] = decimalParts(multiplier)
  const coefficient = principalCoefficient * rateCoefficient
  // Two additional decimal places convert the exact product into integer cents.
  const exponent = principalExponent + rateExponent + 2
  let cents: bigint
  if (exponent >= 0) {
    cents = coefficient * 10n ** BigInt(exponent)
  } else {
    const divisor = 10n ** BigInt(-exponent)
    // Adding half a divisor implements the backend's positive half-up rounding.
    cents = (coefficient + divisor / 2n) / divisor
  }
  // Convert rounded cents only after exact decimal multiplication and rounding.
  return Number(cents) / 100
}

/** isValidRechargeCredit reports whether rounded credits fit positive wallet storage. */
export function isValidRechargeCredit(amount: number): boolean {
  return Number.isFinite(amount) && amount > 0 && amount < BALANCE_CREDIT_CEILING
}

/**
 * decimalParts separates a nonnegative finite number into coefficient and exponent.
 * Callers validate the number first; the result represents coefficient multiplied
 * by ten raised to the returned decimal exponent.
 */
function decimalParts(value: number): [bigint, number] {
  const [mantissa, exponent = '0'] = value.toString().split('e')
  const [whole, fraction = ''] = mantissa.split('.')
  return [BigInt(whole + fraction), Number(exponent) - fraction.length]
}
