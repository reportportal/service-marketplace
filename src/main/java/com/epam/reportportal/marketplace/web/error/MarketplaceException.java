package com.epam.reportportal.marketplace.web.error;

public class MarketplaceException extends RuntimeException {

  private final String code;

  public MarketplaceException(String code, String message) {
    super(message);
    this.code = code;
  }

  public String getCode() {
    return code;
  }
}
