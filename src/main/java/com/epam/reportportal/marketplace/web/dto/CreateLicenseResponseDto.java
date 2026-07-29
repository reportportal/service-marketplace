package com.epam.reportportal.marketplace.web.dto;

import com.epam.reportportal.marketplace.domain.LicenseEntitlement;
import com.epam.reportportal.marketplace.domain.LicensePublicKey;
import java.time.LocalDate;
import java.util.List;

public record CreateLicenseResponseDto(
    String customerId,
    String tier,
    LocalDate issuedAt,
    LocalDate expiresAt,
    List<LicensePublicKey> publicKeys,
    String privateKey) {

  public static CreateLicenseResponseDto from(LicenseEntitlement entitlement, String privateKey) {
    return new CreateLicenseResponseDto(
        entitlement.getCustomerId(),
        entitlement.getTier(),
        entitlement.getIssuedAt(),
        entitlement.getExpiresAt(),
        entitlement.getKeys(),
        privateKey);
  }
}
