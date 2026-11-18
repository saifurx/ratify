import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ErrorMessage } from "@/components/error-message";
import { ApiError, errorMessage, toApiError } from "@/lib/errors";
import { reasonText } from "@/lib/format";

describe("errorMessage", () => {
  it("returns the API's own message, never a generic one", () => {
    expect(errorMessage(new ApiError(403, "this lead cannot be converted: no valid consent basis (no_consent)", "no_consent"))).toBe(
      "this lead cannot be converted: no valid consent basis (no_consent)",
    );
  });
  it("describes network failures specifically", () => {
    expect(errorMessage(new TypeError("fetch failed"))).toMatch(/could not be reached/);
  });
  it("never says 'Something went wrong'", () => {
    for (const e of [new ApiError(500, "internal error"), new TypeError("x"), new Error("boom"), "str", null, undefined]) {
      expect(errorMessage(e)).not.toMatch(/something went wrong/i);
    }
  });
});

describe("toApiError", () => {
  it("reads {error, code} from the response body", async () => {
    const err = await toApiError(new Response(JSON.stringify({ error: "stage does not exist", code: "bad_stage" }), { status: 400 }));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(400);
    expect(err.message).toBe("stage does not exist");
    expect(err.code).toBe("bad_stage");
  });
  it("falls back to the status when the body is not JSON", async () => {
    const err = await toApiError(new Response("<html>bad gateway</html>", { status: 502 }));
    expect(err.message).toBe("Request failed with status 502");
  });
});

describe("<ErrorMessage>", () => {
  it("renders the API message in an alert", () => {
    render(<ErrorMessage error={new ApiError(422, "referenced record does not exist")} />);
    expect(screen.getByRole("alert")).toHaveTextContent("referenced record does not exist");
  });
  it("renders nothing without an error", () => {
    const { container } = render(<ErrorMessage error={null} />);
    expect(container).toBeEmptyDOMElement();
  });
});

describe("reasonText", () => {
  it("explains every consent reason code used by the lead inbox", () => {
    for (const r of ["suppressed", "do_not_contact", "consent_revoked", "no_consent", "insufficient_basis"]) {
      expect(reasonText(r)).not.toMatch(/unknown/);
    }
    expect(reasonText("zzz")).toMatch(/unknown/);
  });
});
