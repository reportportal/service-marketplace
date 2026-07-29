package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.LicenseEntitlement;
import com.epam.reportportal.marketplace.domain.LicensePublicKey;
import java.time.LocalDate;
import java.util.List;

public record LicenseEntitlementDto(
    String customerId,
    String tier,
    LocalDate issuedAt,
    LocalDate expiresAt,
    List<LicensePublicKey> publicKeys) {

  public static LicenseEntitlementDto from(LicenseEntitlement entitlement) {
    return new LicenseEntitlementDto(
        entitlement.getCustomerId(),
        entitlement.getTier(),
        entitlement.getIssuedAt(),
        entitlement.getExpiresAt(),
        entitlement.getKeys());
  }
}
