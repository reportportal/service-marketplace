package com.epam.reportportal.marketplace.web.error;

import com.epam.reportportal.marketplace.web.dto.BlockedArtifactErrorDto;

public class ForbiddenException extends MarketplaceException {

  private final Object payload;

  public ForbiddenException(String message) {
    super("FORBIDDEN", message);
    this.payload = null;
  }

  public ForbiddenException(BlockedArtifactErrorDto blockedPayload) {
    super("FORBIDDEN", "Version is blocked");
    this.payload = blockedPayload;
  }

  public Object getPayload() {
    return payload;
  }
}
