# Converter

Converter to calculate rates for C2F and F2C

## Configuration required

Application will search for config.yaml file

```yaml
httpport: ":3000"
dbusername: postgres
dbpassword: "123"
dbport: "5432"
dbhost: localhost
dbname: rates
exchangeapikey: <api_key>
```

### Database required

```sql
INSERT INTO public.currency(name, symbol, currency_type, is_available)VALUES ('BITCOIN', 'BTC', 'CRYPTO', true);
```
