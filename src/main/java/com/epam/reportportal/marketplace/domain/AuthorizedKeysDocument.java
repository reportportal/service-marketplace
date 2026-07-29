package com.epam.reportportal.marketplace.domain;

import java.util.ArrayList;
import java.util.List;

public class AuthorizedKeysDocument {

  private List<LicenseEntitlement> entitlements = new ArrayList<>();

  public List<LicenseEntitlement> getEntitlements() {
    return entitlements;
  }

  public void setEntitlements(List<LicenseEntitlement> entitlements) {
    this.entitlements = entitlements;
  }
}
