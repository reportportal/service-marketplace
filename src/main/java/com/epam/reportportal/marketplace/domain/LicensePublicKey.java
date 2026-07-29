package com.epam.reportportal.marketplace.domain;

import com.fasterxml.jackson.annotation.JsonAlias;
import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonProperty;
import java.time.LocalDate;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record LicensePublicKey(
    String kid,
    @JsonProperty("publicKey") @JsonAlias("publicKeyPem") String publicKeyPem,
    LocalDate issuedAt) {

  public LicensePublicKey {
    if (kid == null || kid.isBlank()) {
      kid = "default";
    }
  }
}
