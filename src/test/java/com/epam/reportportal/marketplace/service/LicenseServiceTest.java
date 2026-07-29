package com.epam.reportportal.marketplace.service;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.epam.reportportal.marketplace.domain.AuthorizedKeysDocument;
import com.epam.reportportal.marketplace.support.TestStorageFactory;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.CreateLicenseResponseDto;
import com.nimbusds.jose.JWSAlgorithm;
import com.nimbusds.jose.JWSHeader;
import com.nimbusds.jose.crypto.Ed25519Signer;
import com.nimbusds.jose.crypto.Ed25519Verifier;
import com.nimbusds.jose.jwk.OctetKeyPair;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.SignedJWT;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Base64;
import org.junit.jupiter.api.Test;

class LicenseServiceTest {

  @Test
  void createsEntitlementAndVerifiesLicenseJwt() throws Exception {
    var ctx = TestStorageFactory.create();
    LicenseService licenseService = new LicenseService(ctx.store(), ctx.mapper());

    CreateLicenseResponseDto created = licenseService.createEntitlement("acme-corp", null);
    assertNotNull(created.privateKey());
    assertEquals("acme-corp", created.customerId());
    assertFalse(created.publicKeys().isEmpty());

    AuthorizedKeysDocument stored =
        JsonStore.read(ctx.store(), ctx.mapper(), StoragePaths.AUTH_KEYS, AuthorizedKeysDocument.class);
    assertNotNull(stored);
    assertFalse(stored.getEntitlements().isEmpty());
    assertFalse(stored.getEntitlements().get(0).getKeys().isEmpty());

    OctetKeyPair privateJwk = OctetKeyPair.parse(
        new String(Base64.getDecoder().decode(created.privateKey()), StandardCharsets.UTF_8));
    OctetKeyPair publicJwk = OctetKeyPair.parse(created.publicKeys().get(0).publicKeyPem());

    JWTClaimsSet claims = new JWTClaimsSet.Builder()
        .claim("customerId", "acme-corp")
        .claim("pluginId", "plugin-premium")
        .expirationTime(java.util.Date.from(Instant.now().plusSeconds(60)))
        .build();
    SignedJWT jwt = new SignedJWT(new JWSHeader(JWSAlgorithm.EdDSA), claims);
    jwt.sign(new Ed25519Signer(privateJwk));
    assertTrue(jwt.verify(new Ed25519Verifier(publicJwk)));

    assertDoesNotThrow(() -> licenseService.verifyLicenseJwt(jwt.serialize(), "plugin-premium"));
  }
}
