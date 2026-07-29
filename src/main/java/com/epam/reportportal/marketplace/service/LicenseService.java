package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.domain.AuthorizedKeysDocument;
import com.epam.reportportal.marketplace.domain.LicenseEntitlement;
import com.epam.reportportal.marketplace.domain.LicensePublicKey;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.storage.OptimisticConcurrency;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.CreateLicenseResponseDto;
import com.epam.reportportal.marketplace.web.dto.LicenseEntitlementDto;
import com.epam.reportportal.marketplace.web.dto.LicenseEntitlementListResponseDto;
import com.epam.reportportal.marketplace.web.dto.RotateLicenseKeyResponseDto;
import com.epam.reportportal.marketplace.web.error.ConflictException;
import com.epam.reportportal.marketplace.web.error.NotFoundException;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import tools.jackson.databind.ObjectMapper;
import com.nimbusds.jose.crypto.Ed25519Verifier;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.OctetKeyPair;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.SignedJWT;
import java.time.Instant;
import java.time.LocalDate;
import java.time.ZoneOffset;
import java.util.Base64;
import java.util.List;
import java.util.UUID;
import org.springframework.stereotype.Service;

@Service
public class LicenseService {

  private final ObjectStore objectStore;
  private final ObjectMapper objectMapper;

  public LicenseService(ObjectStore objectStore, ObjectMapper objectMapper) {
    this.objectStore = objectStore;
    this.objectMapper = objectMapper;
  }

  public LicenseEntitlementListResponseDto listEntitlements() {
    AuthorizedKeysDocument doc = loadDocument();
    List<LicenseEntitlementDto> items = doc.getEntitlements().stream()
        .map(LicenseEntitlementDto::from)
        .toList();
    return new LicenseEntitlementListResponseDto(items);
  }

  public CreateLicenseResponseDto createEntitlement(String customerId, LocalDate expiresAt) {
    return OptimisticConcurrency.execute(() -> {
      AuthorizedKeysDocument doc = loadDocument();
      if (doc.getEntitlements().stream().anyMatch(e -> customerId.equals(e.getCustomerId()))) {
        throw new ConflictException("Entitlement already exists: " + customerId);
      }
      KeyMaterial keyMaterial = generateKeyPair();
      LicenseEntitlement entitlement = new LicenseEntitlement();
      entitlement.setCustomerId(customerId);
      entitlement.setTier("premium");
      entitlement.setCreatedAt(Instant.now());
      entitlement.setExpiresAt(expiresAt);
      entitlement.getKeys().add(new LicensePublicKey(
          keyMaterial.kid(), keyMaterial.publicKeyJwk(), LocalDate.now(ZoneOffset.UTC)));
      doc.getEntitlements().add(entitlement);
      saveDocument(doc);
      return CreateLicenseResponseDto.from(entitlement, keyMaterial.privateKeyJwkBase64());
    });
  }

  public void revokeEntitlement(String customerId) {
    OptimisticConcurrency.execute(() -> {
      AuthorizedKeysDocument doc = loadDocument();
      boolean removed = doc.getEntitlements().removeIf(e -> customerId.equals(e.getCustomerId()));
      if (!removed) {
        throw new NotFoundException("Entitlement not found: " + customerId);
      }
      saveDocument(doc);
      return null;
    });
  }

  public RotateLicenseKeyResponseDto rotateKey(String customerId) {
    return OptimisticConcurrency.execute(() -> {
      AuthorizedKeysDocument doc = loadDocument();
      LicenseEntitlement entitlement = doc.getEntitlements().stream()
          .filter(e -> customerId.equals(e.getCustomerId()))
          .findFirst()
          .orElseThrow(() -> new NotFoundException("Entitlement not found: " + customerId));
      KeyMaterial keyMaterial = generateKeyPair();
      entitlement.getKeys().add(new LicensePublicKey(
          keyMaterial.kid(), keyMaterial.publicKeyJwk(), LocalDate.now(ZoneOffset.UTC)));
      saveDocument(doc);
      return new RotateLicenseKeyResponseDto(
          customerId, keyMaterial.privateKeyJwkBase64(), keyMaterial.publicKeyJwk());
    });
  }

  public void verifyLicenseJwt(String token, String pluginId) {
    try {
      SignedJWT jwt = SignedJWT.parse(token);
      JWTClaimsSet claims = jwt.getJWTClaimsSet();
      String customerId = claims.getStringClaim("customerId");
      String claimPluginId = claims.getStringClaim("pluginId");
      java.util.Date exp = claims.getExpirationTime();
      if (customerId == null || claimPluginId == null || exp == null) {
        throw new UnauthorizedException("Invalid license JWT claims");
      }
      if (!pluginId.equals(claimPluginId)) {
        throw new UnauthorizedException("License JWT pluginId mismatch");
      }
      if (exp.toInstant().isBefore(Instant.now())) {
        throw new UnauthorizedException("License JWT expired");
      }
      AuthorizedKeysDocument doc = loadDocument();
      LicenseEntitlement entitlement = doc.getEntitlements().stream()
          .filter(e -> customerId.equals(e.getCustomerId()))
          .findFirst()
          .orElseThrow(() -> new UnauthorizedException("Unknown customer entitlement"));
      if (entitlement.getExpiresAt() != null
          && entitlement.getExpiresAt().isBefore(LocalDate.now(ZoneOffset.UTC))) {
        throw new UnauthorizedException("Entitlement expired");
      }
      boolean verified = false;
      for (LicensePublicKey key : entitlement.getKeys()) {
        OctetKeyPair publicJwk = OctetKeyPair.parse(key.publicKeyPem());
        if (jwt.verify(new Ed25519Verifier(publicJwk))) {
          verified = true;
          break;
        }
      }
      if (!verified) {
        throw new UnauthorizedException("License JWT signature invalid");
      }
    } catch (UnauthorizedException e) {
      throw e;
    } catch (Exception e) {
      throw new UnauthorizedException("Invalid license JWT");
    }
  }

  private AuthorizedKeysDocument loadDocument() {
    AuthorizedKeysDocument doc =
        JsonStore.read(objectStore, objectMapper, StoragePaths.AUTH_KEYS, AuthorizedKeysDocument.class);
    if (doc == null) {
      doc = new AuthorizedKeysDocument();
    }
    return doc;
  }

  private void saveDocument(AuthorizedKeysDocument doc) {
    String path = StoragePaths.AUTH_KEYS;
    long generation = objectStore.getGeneration(path).orElse(-1L);
    JsonStore.writeIfGenerationMatch(objectStore, objectMapper, path, doc, generation);
  }

  private KeyMaterial generateKeyPair() {
    try {
      String kid = UUID.randomUUID().toString();
      OctetKeyPair privateJwk = new com.nimbusds.jose.jwk.gen.OctetKeyPairGenerator(Curve.Ed25519)
          .keyID(kid)
          .generate();
      OctetKeyPair publicJwk = privateJwk.toPublicJWK();
      String privateJwkBase64 = Base64.getEncoder().encodeToString(privateJwk.toJSONString().getBytes());
      return new KeyMaterial(kid, privateJwkBase64, publicJwk.toJSONString());
    } catch (Exception e) {
      throw new IllegalStateException("Failed to generate Ed25519 key pair", e);
    }
  }

  private record KeyMaterial(String kid, String privateKeyJwkBase64, String publicKeyJwk) {}
}
